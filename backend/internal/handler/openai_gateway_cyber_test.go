package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newTestGinContext builds a bare gin.Context backed by an httptest recorder.
func newTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c
}

type cyberPolicyHandlerTestCache struct {
	mu      sync.Mutex
	blocked map[string]bool
	ttls    map[string]time.Duration
}

var _ service.GatewayCache = (*cyberPolicyHandlerTestCache)(nil)
var _ service.CyberSessionBlockStore = (*cyberPolicyHandlerTestCache)(nil)

func (c *cyberPolicyHandlerTestCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, nil
}
func (c *cyberPolicyHandlerTestCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (c *cyberPolicyHandlerTestCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (c *cyberPolicyHandlerTestCache) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}
func (c *cyberPolicyHandlerTestCache) SetCyberSessionBlocked(_ context.Context, key string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked == nil {
		c.blocked = make(map[string]bool)
	}
	if c.ttls == nil {
		c.ttls = make(map[string]time.Duration)
	}
	c.blocked[key] = true
	c.ttls[key] = ttl
	return nil
}
func (c *cyberPolicyHandlerTestCache) IsCyberSessionBlocked(_ context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.blocked[key], nil
}
func (c *cyberPolicyHandlerTestCache) ttl(key string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ttls[key]
}

func newCyberPolicyHandlerGatewayService(cache service.GatewayCache) *service.OpenAIGatewayService {
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyCyberSessionBlockEnabled:    "true",
		service.SettingKeyCyberSessionBlockTTLSeconds: "36000",
	}}, nil)
	return service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, cache, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil,
	)
}

// TestRecordCyberPolicyIfMarked_NoMark verifies that when no cyber mark is set,
// the function returns immediately and does NOT set the recorded flag.
func TestRecordCyberPolicyIfMarked_NoMark(t *testing.T) {
	c := newTestGinContext()
	h := &OpenAIGatewayHandler{}

	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", service.ContentModerationProtocolOpenAIResponses, nil, true, "", service.ChannelUsageFields{}, "")

	// Flag must NOT be set when there was no mark.
	require.False(t, c.GetBool(cyberPolicyRecordedKey),
		"cyberPolicyRecordedKey must remain false when no cyber mark is present")
}

// TestRecordCyberPolicyIfMarked_WithMark verifies that:
//  1. When a cyber mark is present, the recorded flag is set (guard activated).
//  2. A second call is a no-op (idempotent guard).
//  3. Nil services do not panic.
func TestRecordCyberPolicyIfMarked_WithMark(t *testing.T) {
	c := newTestGinContext()
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{
		Message:        "flagged",
		Body:           `{"error":{"code":"cyber_policy"}}`,
		UpstreamStatus: 400,
	})

	h := &OpenAIGatewayHandler{} // nil services — must not panic

	// First call: should set the flag.
	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", service.ContentModerationProtocolOpenAIResponses, nil, true, "", service.ChannelUsageFields{}, "")
	})
	require.True(t, c.GetBool(cyberPolicyRecordedKey),
		"cyberPolicyRecordedKey must be true after first call with a mark")

	// Second call: flag already set — must be a no-op (idempotent).
	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", service.ContentModerationProtocolOpenAIResponses, nil, false, "", service.ChannelUsageFields{}, "")
	})
	// Flag should still be true (not toggled or cleared).
	require.True(t, c.GetBool(cyberPolicyRecordedKey),
		"cyberPolicyRecordedKey must remain true after second call (guard)")
}

func TestRecordCyberPolicyIfMarked_PersistsRequestBeforeReturning(t *testing.T) {
	c := newTestGinContext()
	c.Writer.Header().Set("X-Request-Id", "req-sync-audit")
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{
		Message:        "flagged",
		UpstreamStatus: 400,
	})

	repo := &contentModerationHandlerTestRepo{}
	settings := &contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyRiskControlEnabled: "false",
	}}
	moderation := service.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil)
	h := &OpenAIGatewayHandler{contentModerationService: moderation}
	body := []byte(`{"model":"gpt-5","input":[{"role":"user","content":"request retained for audit"},{"type":"function_call_output","output":"complete tool output"}],"temperature":1.2300}`)

	h.recordCyberPolicyIfMarked(
		c, nil, nil, nil, "gpt-5",
		service.ContentModerationProtocolOpenAIResponses, body,
		true, "", service.ChannelUsageFields{}, "",
	)

	logs := repo.logSnapshot()
	require.Len(t, logs, 1, "audit persistence must finish before the handler helper returns")
	require.Equal(t, "req-sync-audit", logs[0].RequestID)
	require.Equal(t, service.ContentModerationProtocolOpenAIResponses, logs[0].CyberRequestProtocol)
	require.Equal(t, string(body), logs[0].CyberRequestSnapshot)
}

// TestRecordCyberPolicyIfMarked_ForwardSuccessSkipsUsageLog verifies the semantic:
// when forwardErrored=false the function still sets the guard flag (mark present),
// but the cyber usage row is NOT requested (only RecordCyberPolicyEvent fires).
// Since services are nil here we only verify the guard flag and no panic.
func TestRecordCyberPolicyIfMarked_ForwardSuccessSkipsUsageLog(t *testing.T) {
	c := newTestGinContext()
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{
		Message:        "flagged",
		UpstreamStatus: 200,
	})

	h := &OpenAIGatewayHandler{}

	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", service.ContentModerationProtocolOpenAIResponses, nil, false /* forwardErrored=false */, "", service.ChannelUsageFields{}, "")
	})
	require.True(t, c.GetBool(cyberPolicyRecordedKey))
}

// TestClearCyberPolicyTurnState verifies F1 at the handler level: after a turn
// is finalized, both the mark and the recorded guard are reset so the next WS
// turn detects/records independently.
func TestClearCyberPolicyTurnState(t *testing.T) {
	c := newTestGinContext()
	h := &OpenAIGatewayHandler{}

	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "turn1", UpstreamStatus: 200})
	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", service.ContentModerationProtocolOpenAIResponses, nil, false, "", service.ChannelUsageFields{}, "")
	require.True(t, c.GetBool(cyberPolicyRecordedKey))

	clearCyberPolicyTurnState(c)
	require.Nil(t, service.GetOpsCyberPolicy(c))
	require.False(t, c.GetBool(cyberPolicyRecordedKey))

	// turn2: a fresh cyber hit must be recordable again.
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "turn2", UpstreamStatus: 200})
	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", service.ContentModerationProtocolOpenAIResponses, nil, false, "", service.ChannelUsageFields{}, "")
	require.True(t, c.GetBool(cyberPolicyRecordedKey))
	require.Equal(t, "turn2", service.GetOpsCyberPolicy(c).Message)
}

// TestBuildCyberSessionBlockedOpsEntry verifies the locally-rejected request is
// auditable: 403 / phase=request / type=cyber_policy_session_blocked — distinct
// from upstream cyber_policy hits, and it must NOT touch moderation/violation.
func TestBuildCyberSessionBlockedOpsEntry(t *testing.T) {
	entry := buildCyberSessionBlockedOpsEntry(cyberPolicyOpsErrorMeta{
		RequestID: "req-9", Model: "gpt-5", RequestPath: "/openai/v1/responses",
	})
	require.Equal(t, 403, entry.StatusCode)
	require.Equal(t, "cyber_policy_session_blocked", entry.ErrorType)
	require.Equal(t, "request", entry.ErrorPhase)
	require.True(t, entry.IsBusinessLimited)
	require.Equal(t, "gateway_local", entry.ErrorSource)
	require.Equal(t, "platform", entry.ErrorOwner)
	require.Empty(t, entry.ErrorBody, "no session block key → ErrorBody must be empty")

	entryWithKey := buildCyberSessionBlockedOpsEntry(cyberPolicyOpsErrorMeta{
		RequestID: "req-9", Model: "gpt-5", RequestPath: "/openai/v1/responses",
		SessionBlockKey: "abc123",
	})
	require.Equal(t, "session_block_key=abc123", entryWithKey.ErrorBody)
}

// TestRejectIfCyberSessionBlocked_FailOpen verifies fail-open paths: nil handler
// services and requests without either a session identity or user text pass
// through.
func TestRejectIfCyberSessionBlocked_FailOpen(t *testing.T) {
	c := newTestGinContext()
	c.Request = httptest.NewRequest("POST", "/openai/v1/responses", strings.NewReader(`{}`))

	h := &OpenAIGatewayHandler{}
	require.False(t, h.rejectIfCyberSessionBlocked(c, nil, service.ContentModerationProtocolOpenAIResponses, []byte(`{}`), "gpt-5", cyberBlockFormatResponses), "nil apiKey → pass")

	h2 := &OpenAIGatewayHandler{gatewayService: nil}
	key := &service.APIKey{ID: 1}
	require.False(t, h2.rejectIfCyberSessionBlocked(c, key, service.ContentModerationProtocolOpenAIResponses, []byte(`{}`), "gpt-5", cyberBlockFormatResponses), "nil gateway service → pass")
}

func TestRejectIfCyberSessionBlocked_UsesMessageFallback(t *testing.T) {
	cache := &cyberPolicyHandlerTestCache{}
	gateway := newCyberPolicyHandlerGatewayService(cache)
	h := &OpenAIGatewayHandler{gatewayService: gateway}
	apiKey := &service.APIKey{ID: 101}
	body := []byte(`{"model":"gpt-5","input":"same blocked message"}`)

	seedCtx := newTestGinContext()
	seedCtx.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(string(body)))
	blockKey := service.CyberPolicyBlockKey(apiKey.ID, seedCtx, service.ContentModerationProtocolOpenAIResponses, body)
	require.NotEmpty(t, blockKey)
	gateway.MarkCyberSessionBlocked(seedCtx.Request.Context(), blockKey)

	request := func(key *service.APIKey, requestBody []byte) (*httptest.ResponseRecorder, bool) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(string(requestBody)))
		return recorder, h.rejectIfCyberSessionBlocked(
			c,
			key,
			service.ContentModerationProtocolOpenAIResponses,
			requestBody,
			"gpt-5",
			cyberBlockFormatResponses,
		)
	}

	blockedRecorder, blocked := request(apiKey, body)
	require.True(t, blocked)
	require.Equal(t, http.StatusForbidden, blockedRecorder.Code)
	require.Contains(t, blockedRecorder.Header().Get("Content-Type"), "application/json")
	require.Contains(t, blockedRecorder.Body.String(), `"code":"session_blocked_by_cyber_policy"`)

	otherMessageRecorder, blocked := request(apiKey, []byte(`{"model":"gpt-5","input":"different message"}`))
	require.False(t, blocked)
	require.Equal(t, http.StatusOK, otherMessageRecorder.Code)

	otherKeyRecorder, blocked := request(&service.APIKey{ID: 202}, body)
	require.False(t, blocked)
	require.Equal(t, http.StatusOK, otherKeyRecorder.Code)
}

// TestRecordCyberPolicyIfMarked_BlockKeyPlumbed verifies the 6th param is
// accepted and a non-empty key with nil gateway service does not panic
// (write-side guards live in the service layer).
func TestRecordCyberPolicyIfMarked_BlockKeyPlumbed(t *testing.T) {
	c := newTestGinContext()
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "x", UpstreamStatus: 400})
	h := &OpenAIGatewayHandler{}
	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", service.ContentModerationProtocolOpenAIResponses, nil, true, "deadbeef", service.ChannelUsageFields{}, "")
	})
}

func TestRecordCyberPolicyIfMarked_WritesBlockBeforeReturning(t *testing.T) {
	cache := &cyberPolicyHandlerTestCache{}
	gateway := newCyberPolicyHandlerGatewayService(cache)
	c := newTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "blocked", UpstreamStatus: 400})

	const blockKey = "message:v1:synchronous-write"
	h := &OpenAIGatewayHandler{gatewayService: gateway}
	h.recordCyberPolicyIfMarked(
		c,
		&service.APIKey{ID: 101},
		nil,
		nil,
		"gpt-5",
		service.ContentModerationProtocolOpenAIResponses,
		nil,
		true,
		blockKey,
		service.ChannelUsageFields{},
		"",
	)

	require.True(t, gateway.IsCyberSessionBlocked(context.Background(), blockKey))
	require.Equal(t, 10*time.Hour, cache.ttl(blockKey))
}

func TestOpenAIWSCyberBlockKeyResolver_UpdatesAndInheritsExplicitIdentity(t *testing.T) {
	c := newTestGinContext()
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	resolver := newOpenAIWSCyberBlockKeyResolver(101, c)

	first := []byte(`{"type":"response.create","prompt_cache_key":"session-a","response":{"input":"first"}}`)
	second := []byte(`{"type":"response.create","prompt_cache_key":"session-b","response":{"input":"second"}}`)
	withoutIdentity := []byte(`{"type":"response.create","response":{"input":"third"}}`)

	firstKey := resolver.Resolve(first)
	secondKey := resolver.Resolve(second)
	require.Equal(t, service.CyberSessionBlockKey(101, c, first), firstKey)
	require.Equal(t, service.CyberSessionBlockKey(101, c, second), secondKey)
	require.NotEqual(t, firstKey, secondKey)
	require.Equal(t, secondKey, resolver.Resolve(withoutIdentity), "a frame without identity must inherit the latest explicit session")
}

func TestOpenAIWSCyberBlockKeyResolver_FrameIdentityOverridesHandshakeAndIsInherited(t *testing.T) {
	c := newTestGinContext()
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	c.Request.Header.Set("session-id", "handshake-session")
	resolver := newOpenAIWSCyberBlockKeyResolver(101, c)

	withoutIdentity := []byte(`{"type":"response.create","response":{"input":"message"}}`)
	handshakeKey := service.CyberSessionBlockKey(101, c, nil)
	require.NotEmpty(t, handshakeKey)
	require.Equal(t, handshakeKey, resolver.Resolve(withoutIdentity))

	frame := []byte(`{"type":"response.create","prompt_cache_key":"frame-session","response":{"input":"stateful"}}`)
	frameKey := service.CyberSessionBlockKey(101, nil, frame)
	require.NotEmpty(t, frameKey)
	require.NotEqual(t, handshakeKey, frameKey)
	require.Equal(t, frameKey, resolver.Resolve(frame))
	require.Equal(t, frameKey, resolver.Resolve(withoutIdentity), "an identity-less frame must inherit the latest frame identity, not the handshake header")
}

func TestOpenAIWSCyberBlockKeyResolver_UsesMessageUntilExplicitIdentityAppears(t *testing.T) {
	c := newTestGinContext()
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	resolver := newOpenAIWSCyberBlockKeyResolver(101, c)
	messageOnly := []byte(`{"type":"response.create","response":{"input":"stateless message"}}`)

	require.Equal(t,
		service.CyberPolicyBlockKey(101, c, service.ContentModerationProtocolOpenAIResponses, messageOnly),
		resolver.Resolve(messageOnly),
	)

	explicit := []byte(`{"type":"response.create","prompt_cache_key":"session-a","response":{"input":"stateful"}}`)
	explicitKey := resolver.Resolve(explicit)
	require.NotEmpty(t, explicitKey)
	require.Equal(t, explicitKey, resolver.Resolve(messageOnly))
}

func TestOpenAIResponsesWebSocket_MessageFallbackBlocksFirstFrame(t *testing.T) {
	cache := &cyberPolicyHandlerTestCache{}
	gateway := newCyberPolicyHandlerGatewayService(cache)
	payload := []byte(`{"type":"response.create","model":"gpt-5","response":{"input":"blocked websocket message"}}`)
	seedCtx := newTestGinContext()
	seedCtx.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	blockKey := service.CyberPolicyBlockKey(101, seedCtx, service.ContentModerationProtocolOpenAIResponses, payload)
	require.NotEmpty(t, blockKey)
	gateway.MarkCyberSessionBlocked(seedCtx.Request.Context(), blockKey)

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.gatewayService = gateway
	server := newOpenAIWSHandlerTestServer(t, h, middleware.AuthSubject{UserID: 1, Concurrency: 1})
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	conn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = conn.Write(writeCtx, coderws.MessageText, payload)
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, message, err := conn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Contains(t, string(message), `"code":"session_blocked_by_cyber_policy"`)

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = conn.Read(closeCtx)
	cancelClose()
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
}

// TestBuildCyberPolicyOpsErrorEntry_StatusCode verifies F6: the ops error log
// records the status the codex client actually received (400 non-stream / 200 stream),
// not a hardcoded 403.
func TestBuildCyberPolicyOpsErrorEntry_StatusCode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		upstreamStatus int
	}{
		{"non_stream_400", 400},
		{"stream_200", 200},
		{"zero_value", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mark := &service.CyberPolicyMark{
				Code:           "cyber_policy",
				Message:        "blocked",
				UpstreamStatus: tc.upstreamStatus,
			}
			entry := buildCyberPolicyOpsErrorEntry(cyberPolicyOpsErrorMeta{
				RequestID: "req-1", Model: "gpt-5", RequestPath: "/openai/v1/responses",
			}, mark)
			require.Equal(t, tc.upstreamStatus, entry.StatusCode)
			require.Equal(t, "cyber_policy", entry.ErrorType)
			require.Equal(t, "request", entry.ErrorPhase)
		})
	}
}
