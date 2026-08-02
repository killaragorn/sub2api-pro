package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keywordSessionTestCache struct {
	mu      sync.Mutex
	blocked map[string]bool
}

var _ service.GatewayCache = (*keywordSessionTestCache)(nil)
var _ service.KeywordSessionBlockStore = (*keywordSessionTestCache)(nil)

func (c *keywordSessionTestCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, nil
}
func (c *keywordSessionTestCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (c *keywordSessionTestCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (c *keywordSessionTestCache) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}
func (c *keywordSessionTestCache) ClaimKeywordSessionBlocked(_ context.Context, key string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked == nil {
		c.blocked = make(map[string]bool)
	}
	if c.blocked[key] {
		return false, nil
	}
	c.blocked[key] = true
	return true, nil
}
func (c *keywordSessionTestCache) IsKeywordSessionBlocked(_ context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.blocked[key], nil
}

type keywordSessionFailOpenCache struct {
	keywordSessionTestCache
}

func (c *keywordSessionFailOpenCache) ClaimKeywordSessionBlocked(context.Context, string, time.Duration) (bool, error) {
	return false, errors.New("redis unavailable")
}
func (c *keywordSessionFailOpenCache) IsKeywordSessionBlocked(context.Context, string) (bool, error) {
	return false, errors.New("redis unavailable")
}

type keywordSessionLegacyEngine struct {
	calls       int
	blockOnCall int
}

func (e *keywordSessionLegacyEngine) Check(context.Context, securityaudit.Request) (*securityaudit.LegacyDecision, error) {
	e.calls++
	if e.blockOnCall > 0 && e.calls != e.blockOnCall {
		return &securityaudit.LegacyDecision{Allowed: true, Action: service.ContentModerationActionAllow}, nil
	}
	return &securityaudit.LegacyDecision{
		Blocked: true, Flagged: true, StatusCode: http.StatusForbidden,
		ErrorCode: "content_policy_violation", Message: "keyword blocked",
		Action: service.ContentModerationActionKeywordBlock,
	}, nil
}

func newKeywordSessionGatewayService(cache service.GatewayCache) *service.OpenAIGatewayService {
	return service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, cache, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
}

func newKeywordSessionModerationService(t *testing.T) (*service.ContentModerationService, *contentModerationHandlerTestRepo, *contentModerationHandlerSettingRepo) {
	t.Helper()
	cfg := &service.ContentModerationConfig{
		Enabled: true, Mode: service.ContentModerationModePreBlock,
		AllGroups: true, BlockStatus: http.StatusForbidden,
		BlockMessage: "keyword blocked", KeywordBlockingMode: service.ContentModerationKeywordModeKeywordOnly,
		BlockedKeywords: []string{"blocked"},
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings := &contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyRiskControlEnabled:      "true",
		service.SettingKeyContentModerationConfig: string(raw),
	}}
	repo := &contentModerationHandlerTestRepo{}
	return service.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil), repo, settings
}

func keywordSessionAuditContext(t *testing.T, sessionID string) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"blocked"}`))
	c.Request.Header.Set("session-id", sessionID)
	return c
}

func TestOpenAISecurityAuditKeywordBlockStopsSameSessionBeforeReaudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	apiKey := &service.APIKey{ID: 42}
	body := []byte(`{"model":"gpt-5","input":"blocked"}`)

	first := h.checkSecurityAudit(
		keywordSessionAuditContext(t, "session-a"), nil, apiKey,
		middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses,
		"gpt-5", body,
	)
	require.True(t, isKeywordBlockDecision(first))
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)

	second := h.checkSecurityAudit(
		keywordSessionAuditContext(t, "session-a"), nil, apiKey,
		middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses,
		"gpt-5", body,
	)
	require.Equal(t, keywordSessionBlockedErrorCode, second.ErrorCode)
	require.Equal(t, http.StatusBadRequest, second.HTTPStatus)
	time.Sleep(20 * time.Millisecond)
	require.Len(t, repo.logSnapshot(), 1, "blocked session must stop before another keyword log is created")

	third := h.checkSecurityAudit(
		keywordSessionAuditContext(t, "session-b"), nil, apiKey,
		middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses,
		"gpt-5", body,
	)
	require.True(t, isKeywordBlockDecision(third))
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 2 }, time.Second, time.Millisecond,
		"a different explicit session must remain independently auditable")
}

func TestOpenAISecurityAuditWithoutExplicitSessionDoesNotPersistBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	apiKey := &service.APIKey{ID: 42}
	body := []byte(`{"model":"gpt-5","input":"blocked"}`)

	for range 2 {
		decision := h.checkSecurityAudit(keywordSessionAuditContext(t, ""), nil, apiKey,
			middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses,
			"gpt-5", body)
		require.True(t, isKeywordBlockDecision(decision))
	}
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 2 }, time.Second, time.Millisecond,
		"without an explicit session identifier, each request must be audited independently")
}

func TestOpenAIResponsesKeywordSessionBlockIsTerminalBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		billingCacheService:      &service.BillingCacheService{},
		apiKeyService:            &service.APIKeyService{},
		concurrencyHelper:        NewConcurrencyHelper(&service.ConcurrencyService{}, SSEPingFormatNone, time.Second),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	groupID := int64(2)
	apiKey := &service.APIKey{
		ID: 42, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI},
		User: &service.User{ID: 9},
	}
	run := func() (*httptest.ResponseRecorder, map[string]any) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
			`{"model":"gpt-5","input":"blocked"}`,
		))
		c.Request.Header.Set("session-id", "handler-session")
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 9, Concurrency: 1})
		h.Responses(c)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
		return recorder, payload
	}

	first, firstPayload := run()
	require.Equal(t, http.StatusBadRequest, first.Code)
	require.Equal(t, "no-store", first.Header().Get("Cache-Control"))
	require.Equal(t, "content_policy_violation", requireObject(t, firstPayload["error"])["code"])
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)

	second, secondPayload := run()
	require.Equal(t, http.StatusBadRequest, second.Code)
	require.Equal(t, keywordSessionBlockedErrorCode, requireObject(t, secondPayload["error"])["code"])
	time.Sleep(20 * time.Millisecond)
	require.Len(t, repo.logSnapshot(), 1)
}

func TestOpenAIResponsesWebSocketKeywordBlockIsTerminalAndReconnectDeduplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		billingCacheService:      &service.BillingCacheService{},
		apiKeyService:            &service.APIKeyService{},
		concurrencyHelper:        NewConcurrencyHelper(service.NewConcurrencyService(&concurrencyCacheMock{}), SSEPingFormatNone, time.Second),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	wsServer := newOpenAIWSHandlerTestServer(t, h, middleware2.AuthSubject{UserID: 9, Concurrency: 1})
	defer wsServer.Close()
	url := "ws" + strings.TrimPrefix(wsServer.URL, "http") + "/openai/v1/responses"
	payload := []byte(`{"type":"response.create","model":"gpt-5","prompt_cache_key":"ws-keyword-session","input":"blocked"}`)

	run := func() (map[string]any, coderws.CloseError) {
		dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
		conn, _, err := coderws.Dial(dialCtx, url, nil)
		cancelDial()
		require.NoError(t, err)
		defer func() { _ = conn.CloseNow() }()

		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		err = conn.Write(writeCtx, coderws.MessageText, payload)
		cancelWrite()
		require.NoError(t, err)

		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		_, wirePayload, err := conn.Read(readCtx)
		cancelRead()
		require.NoError(t, err)
		var frame map[string]any
		require.NoError(t, json.Unmarshal(wirePayload, &frame))

		closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
		_, _, err = conn.Read(closeCtx)
		cancelClose()
		var closeErr coderws.CloseError
		require.ErrorAs(t, err, &closeErr)
		return frame, closeErr
	}

	first, firstClose := run()
	require.Equal(t, "error", first["type"])
	require.Equal(t, float64(http.StatusBadRequest), first["status"])
	require.Equal(t, "invalid_prompt", requireObject(t, first["error"])["code"])
	require.Equal(t, coderws.StatusPolicyViolation, firstClose.Code)
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)

	second, secondClose := run()
	require.Equal(t, "error", second["type"])
	require.Equal(t, float64(http.StatusBadRequest), second["status"])
	require.Equal(t, "invalid_prompt", requireObject(t, second["error"])["code"])
	require.Equal(t, coderws.StatusPolicyViolation, secondClose.Code)
	time.Sleep(20 * time.Millisecond)
	require.Len(t, repo.logSnapshot(), 1, "a reconnect in the blocked session must stop before another audit")
}

func TestOpenAIResponsesStreamingKeywordSessionBlockUsesInvalidPromptTerminalEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		billingCacheService:      &service.BillingCacheService{},
		apiKeyService:            &service.APIKeyService{},
		concurrencyHelper:        NewConcurrencyHelper(&service.ConcurrencyService{}, SSEPingFormatNone, time.Second),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	groupID := int64(2)
	apiKey := &service.APIKey{
		ID: 42, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI},
		User: &service.User{ID: 9},
	}
	run := func() (*httptest.ResponseRecorder, map[string]any) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
			`{"model":"gpt-5","input":"blocked","stream":true}`,
		))
		c.Request.Header.Set("session-id", "handler-stream-session")
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 9, Concurrency: 1})
		h.Responses(c)
		_, errorObject := parseResponsesFailedSSE(t, recorder.Body.String())
		return recorder, errorObject
	}

	first, firstError := run()
	require.Equal(t, http.StatusOK, first.Code)
	require.Contains(t, first.Header().Get("Content-Type"), "text/event-stream")
	require.Equal(t, "invalid_request_error", firstError["type"])
	require.Equal(t, "invalid_prompt", firstError["code"])
	require.Equal(t, "keyword blocked", firstError["message"])
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)

	second, secondError := run()
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "invalid_request_error", secondError["type"])
	require.Equal(t, "invalid_prompt", secondError["code"])
	require.Equal(t, keywordSessionBlockedClientMsg, secondError["message"])
	time.Sleep(20 * time.Millisecond)
	require.Len(t, repo.logSnapshot(), 1)
}

func TestOpenAIResponsesBodySignalCompactKeywordBlockUsesInvalidPromptTerminalEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		billingCacheService:      &service.BillingCacheService{},
		apiKeyService:            &service.APIKeyService{},
		concurrencyHelper:        NewConcurrencyHelper(&service.ConcurrencyService{}, SSEPingFormatNone, time.Second),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	groupID := int64(2)
	apiKey := &service.APIKey{
		ID: 42, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI},
		User: &service.User{ID: 9},
	}
	run := func() (*httptest.ResponseRecorder, map[string]any) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
			`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"blocked"},{"type":"compaction_trigger"}],"stream":true}`,
		))
		c.Request.Header.Set("session-id", "handler-compact-stream-session")
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 9, Concurrency: 1})
		h.Responses(c)
		_, errorObject := parseResponsesFailedSSE(t, recorder.Body.String())
		return recorder, errorObject
	}

	first, firstError := run()
	require.Equal(t, http.StatusOK, first.Code)
	require.Contains(t, first.Header().Get("Content-Type"), "text/event-stream")
	require.Equal(t, "invalid_request_error", firstError["type"])
	require.Equal(t, "invalid_prompt", firstError["code"])
	require.Equal(t, "keyword blocked", firstError["message"])
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)

	second, secondError := run()
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, "invalid_request_error", secondError["type"])
	require.Equal(t, "invalid_prompt", secondError["code"])
	require.Equal(t, keywordSessionBlockedClientMsg, secondError["message"])
	time.Sleep(20 * time.Millisecond)
	require.Len(t, repo.logSnapshot(), 1)
}

func TestOpenAISecurityAuditConcurrentKeywordBlockCreatesOneLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	apiKey := &service.APIKey{ID: 42}
	body := []byte(`{"model":"gpt-5","input":"blocked"}`)

	const requests = 12
	start := make(chan struct{})
	decisions := make(chan *securityaudit.Decision, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			decisions <- h.checkSecurityAudit(
				keywordSessionAuditContext(t, "session-concurrent"), nil, apiKey,
				middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses,
				"gpt-5", body,
			)
		}()
	}
	close(start)
	wg.Wait()
	close(decisions)

	keywordBlocks := 0
	sessionBlocks := 0
	for decision := range decisions {
		if isKeywordBlockDecision(decision) {
			keywordBlocks++
		} else if decision != nil && decision.ErrorCode == keywordSessionBlockedErrorCode {
			sessionBlocks++
		}
	}
	require.Equal(t, 1, keywordBlocks)
	require.Equal(t, requests-1, sessionBlocks)
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	logs := repo.logSnapshot()
	require.Len(t, logs, 1)
	require.Equal(t, service.ContentModerationActionKeywordBlock, logs[0].Action)
}

func TestOpenAISecurityAuditKeywordSessionCacheFailureIsFailOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionFailOpenCache{}
	moderation, repo, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	decision := h.checkSecurityAudit(keywordSessionAuditContext(t, "fail-open-session"), nil, &service.APIKey{ID: 42},
		middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses, "gpt-5",
		[]byte(`{"model":"gpt-5","input":"blocked"}`))
	require.True(t, isKeywordBlockDecision(decision), "cache failure must preserve the original moderation decision")
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)
}

func TestOpenAISecurityAuditWebSocketTurnTracksCurrentExplicitSessionKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	legacy := &keywordSessionLegacyEngine{blockOnCall: 2}
	moderation, _, _ := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(legacy, nil),
	}
	apiKey := &service.APIKey{ID: 42}
	c := keywordSessionAuditContext(t, "")

	first := h.checkSecurityAuditStage(
		c, nil, apiKey, middleware2.AuthSubject{UserID: 9},
		service.ContentModerationProtocolOpenAIResponses, "gpt-5",
		[]byte(`{"model":"gpt-5","prompt_cache_key":"ws-session-a","input":"allowed shape"}`),
		"first_turn",
	)
	require.True(t, first.AllowNextStage)
	require.Equal(t, 1, legacy.calls)

	second := h.checkSecurityAuditStage(
		c, nil, apiKey, middleware2.AuthSubject{UserID: 9},
		service.ContentModerationProtocolOpenAIResponses, "gpt-5",
		[]byte(`{"model":"gpt-5","prompt_cache_key":"ws-session-b","input":"blocked follow-up"}`),
		"subsequent_turn",
	)
	require.True(t, isKeywordBlockDecision(second))
	require.Equal(t, 2, legacy.calls)

	third := h.checkSecurityAuditStage(
		c, nil, apiKey, middleware2.AuthSubject{UserID: 9},
		service.ContentModerationProtocolOpenAIResponses, "gpt-5",
		[]byte(`{"model":"gpt-5","input":"another follow-up"}`),
		"subsequent_turn",
	)
	require.Equal(t, keywordSessionBlockedErrorCode, third.ErrorCode)
	require.Equal(t, 2, legacy.calls, "a frame without identity must reuse the most recent explicit session key")

	freshA := keywordSessionAuditContext(t, "")
	allowedA := h.checkSecurityAuditStage(
		freshA, nil, apiKey, middleware2.AuthSubject{UserID: 9},
		service.ContentModerationProtocolOpenAIResponses, "gpt-5",
		[]byte(`{"model":"gpt-5","prompt_cache_key":"ws-session-a","input":"allowed retry"}`),
		"first_turn",
	)
	require.True(t, allowedA.AllowNextStage, "switching to session B must not block the previous session A")
	require.Equal(t, 3, legacy.calls)
}

func TestOpenAISecurityAuditKeywordSessionBlockFollowsActivePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &keywordSessionTestCache{}
	moderation, repo, settings := newKeywordSessionModerationService(t)
	h := &OpenAIGatewayHandler{
		gatewayService:           newKeywordSessionGatewayService(cache),
		contentModerationService: moderation,
		securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(moderation), nil),
	}
	apiKey := &service.APIKey{ID: 42}
	body := []byte(`{"model":"gpt-5","input":"blocked"}`)
	policyInput := service.ContentModerationCheckInput{
		Provider: service.PlatformOpenAI, Protocol: service.ContentModerationProtocolOpenAIResponses,
		Model: "gpt-5", Body: body,
	}
	initialPolicy := moderation.KeywordSessionPolicy(context.Background(), policyInput)
	require.True(t, initialPolicy.Active)
	require.True(t, initialPolicy.WouldBlock)

	first := h.checkSecurityAudit(keywordSessionAuditContext(t, "policy-session"), nil, apiKey,
		middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses, "gpt-5", body)
	require.True(t, isKeywordBlockDecision(first))
	require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, time.Millisecond)

	settings.values[service.SettingKeyRiskControlEnabled] = "false"
	time.Sleep(1100 * time.Millisecond)
	require.Eventually(t, func() bool {
		return !moderation.KeywordSessionPolicy(context.Background(), policyInput).Active
	}, time.Second, time.Millisecond)
	disabled := h.checkSecurityAudit(keywordSessionAuditContext(t, "policy-session"), nil, apiKey,
		middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses, "gpt-5", body)
	require.NotNil(t, disabled)
	require.True(t, disabled.AllowNextStage, "disabled risk control must not enforce a historical session block")

	settings.values[service.SettingKeyRiskControlEnabled] = "true"
	cfg := &service.ContentModerationConfig{
		Enabled: true, Mode: service.ContentModerationModePreBlock, AllGroups: true,
		KeywordBlockingMode: service.ContentModerationKeywordModeKeywordOnly,
		BlockedKeywords:     []string{"different-keyword"},
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings.values[service.SettingKeyContentModerationConfig] = string(raw)
	time.Sleep(1100 * time.Millisecond)
	require.Eventually(t, func() bool {
		policy := moderation.KeywordSessionPolicy(context.Background(), policyInput)
		return policy.Active && !policy.WouldBlock && policy.Version != initialPolicy.Version
	}, time.Second, time.Millisecond)
	changed := h.checkSecurityAudit(keywordSessionAuditContext(t, "policy-session"), nil, apiKey,
		middleware2.AuthSubject{UserID: 9}, service.ContentModerationProtocolOpenAIResponses, "gpt-5", body)
	require.NotNil(t, changed)
	require.True(t, changed.AllowNextStage, "a changed keyword policy must not enforce the old policy's session block")
}
