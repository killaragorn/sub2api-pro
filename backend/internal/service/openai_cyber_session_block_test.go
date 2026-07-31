package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCyberBlockTestCtx(headers map[string]string, body string) (*gin.Context, []byte) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/openai/v1/responses", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, []byte(body)
}

// TestCyberSessionBlockKey verifies the explicit-session key used by both
// cyber and keyword policy state.
func TestCyberSessionBlockKey(t *testing.T) {
	c1, b1 := newCyberBlockTestCtx(map[string]string{"session_id": "sess-abc"}, `{}`)
	k1 := CyberSessionBlockKey(101, c1, b1)
	require.NotEmpty(t, k1)

	// Same session, different apiKey → different key (isolation).
	c2, b2 := newCyberBlockTestCtx(map[string]string{"session_id": "sess-abc"}, `{}`)
	require.NotEqual(t, k1, CyberSessionBlockKey(202, c2, b2))

	// Same session + same apiKey → stable key.
	c3, b3 := newCyberBlockTestCtx(map[string]string{"session_id": "sess-abc"}, `{}`)
	require.Equal(t, k1, CyberSessionBlockKey(101, c3, b3))

	// prompt_cache_key in body counts as explicit.
	c4, b4 := newCyberBlockTestCtx(nil, `{"prompt_cache_key":"pck-1"}`)
	require.NotEmpty(t, CyberSessionBlockKey(101, c4, b4))

	// The shared explicit-session primitive stays empty without an identity;
	// CyberPolicyBlockKey adds the message fallback separately.
	c5, b5 := newCyberBlockTestCtx(nil, `{"input":"hello world"}`)
	require.Empty(t, CyberSessionBlockKey(101, c5, b5))

	// conversation_id header counts as explicit; key is stable and non-empty.
	c6, b6 := newCyberBlockTestCtx(map[string]string{"conversation_id": "conv-xyz"}, `{}`)
	k6 := CyberSessionBlockKey(101, c6, b6)
	require.NotEmpty(t, k6)
	c6b, b6b := newCyberBlockTestCtx(map[string]string{"conversation_id": "conv-xyz"}, `{}`)
	require.Equal(t, k6, CyberSessionBlockKey(101, c6b, b6b), "conversation_id key must be stable")

	// Native Codex accepts client_metadata.session_id as the upstream session.
	c7, b7 := newCyberBlockTestCtx(nil, `{"client_metadata":{"session_id":"metadata-session"}}`)
	metadataKey := CyberSessionBlockKey(101, c7, b7)
	require.NotEmpty(t, metadataKey)
	c7b, b7b := newCyberBlockTestCtx(nil, `{"client_metadata":{"session_id":"metadata-session"}}`)
	require.Equal(t, metadataKey, CyberSessionBlockKey(101, c7b, b7b))

	// Match native Codex precedence: prompt_cache_key, official/legacy session
	// headers, client_metadata.session_id, then compatibility headers.
	c8, b8 := newCyberBlockTestCtx(map[string]string{
		"session-id":      "header-session",
		"conversation_id": "conversation-session",
	}, `{"prompt_cache_key":"body-session","client_metadata":{"session_id":"metadata-session"}}`)
	c8Body, b8Body := newCyberBlockTestCtx(nil, `{"prompt_cache_key":"body-session"}`)
	require.Equal(t, CyberSessionBlockKey(101, c8Body, b8Body), CyberSessionBlockKey(101, c8, b8))

	c9, b9 := newCyberBlockTestCtx(map[string]string{
		"session-id": "header-session",
	}, `{"client_metadata":{"session_id":"metadata-session"}}`)
	c9Header, b9Header := newCyberBlockTestCtx(map[string]string{"session-id": "header-session"}, `{}`)
	require.Equal(t, CyberSessionBlockKey(101, c9Header, b9Header), CyberSessionBlockKey(101, c9, b9))

	c10, b10 := newCyberBlockTestCtx(map[string]string{
		"conversation_id": "conversation-session",
	}, `{"client_metadata":{"session_id":"metadata-session"}}`)
	require.Equal(t, metadataKey, CyberSessionBlockKey(101, c10, b10))
}

func TestCyberPolicyBlockKeyFallsBackToLatestRealUserMessage(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		body     string
		minimal  string
		older    string
	}{
		{
			name:     "responses after tool output",
			protocol: ContentModerationProtocolOpenAIResponses,
			body: `{"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"older request"}]},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"latest request"}]},
				{"type":"function_call","name":"lookup"},
				{"type":"function_call_output","output":"result"}
			]}`,
			minimal: `{"input":"latest request"}`,
			older:   `{"input":"older request"}`,
		},
		{
			name:     "responses websocket envelope",
			protocol: ContentModerationProtocolOpenAIResponses,
			body: `{"type":"response.create","response":{"input":[
				{"role":"user","content":[{"type":"input_text","text":"older request"}]},
				{"role":"user","content":[{"type":"input_text","text":"latest request"}]},
				{"type":"function_call_output","output":"result"}
			]}}`,
			minimal: `{"input":"latest request"}`,
			older:   `{"input":"older request"}`,
		},
		{
			name:     "chat after tool output",
			protocol: ContentModerationProtocolOpenAIChat,
			body: `{"messages":[
				{"role":"user","content":"older request"},
				{"role":"assistant","content":"answer"},
				{"role":"user","content":"latest request"},
				{"role":"assistant","tool_calls":[{"function":{"name":"lookup"}}]},
				{"role":"tool","content":"result"}
			]}`,
			minimal: `{"messages":[{"role":"user","content":"latest request"}]}`,
			older:   `{"messages":[{"role":"user","content":"older request"}]}`,
		},
		{
			name:     "chat responses shaped input",
			protocol: ContentModerationProtocolOpenAIChat,
			body: `{"input":[
				{"role":"user","content":[{"type":"input_text","text":"older request"}]},
				{"role":"user","content":[{"type":"input_text","text":"latest request"}]},
				{"type":"function_call_output","output":"result"}
			]}`,
			minimal: `{"input":"latest request"}`,
			older:   `{"input":"older request"}`,
		},
		{
			name:     "anthropic after tool result",
			protocol: ContentModerationProtocolAnthropicMessages,
			body: `{"messages":[
				{"role":"user","content":"older request"},
				{"role":"assistant","content":"answer"},
				{"role":"user","content":"latest request"},
				{"role":"assistant","content":[{"type":"tool_use","name":"lookup"}]},
				{"role":"user","content":[{"type":"tool_result","content":"result"}]}
			]}`,
			minimal: `{"messages":[{"role":"user","content":"latest request"}]}`,
			older:   `{"messages":[{"role":"user","content":"older request"}]}`,
		},
	}

	keys := make(map[string]string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, body := newCyberBlockTestCtx(nil, tt.body)
			key := CyberPolicyBlockKey(101, c, tt.protocol, body)
			minimalCtx, minimalBody := newCyberBlockTestCtx(nil, tt.minimal)
			olderCtx, olderBody := newCyberBlockTestCtx(nil, tt.older)

			require.Contains(t, key, "message:v1:")
			require.Equal(t, CyberPolicyBlockKey(101, minimalCtx, tt.protocol, minimalBody), key)
			require.NotEqual(t, CyberPolicyBlockKey(101, olderCtx, tt.protocol, olderBody), key)
			keys[tt.protocol] = key
		})
	}
	require.NotEqual(t, keys[ContentModerationProtocolOpenAIResponses], keys[ContentModerationProtocolOpenAIChat])
	require.NotEqual(t, keys[ContentModerationProtocolOpenAIChat], keys[ContentModerationProtocolAnthropicMessages])
}

func TestCyberPolicyBlockKeyMessageFallbackIsolationAndNormalization(t *testing.T) {
	c1, b1 := newCyberBlockTestCtx(nil, `{"input":"  repeated\n  message "}`)
	c2, b2 := newCyberBlockTestCtx(nil, `{"input":"repeated message"}`)
	c3, b3 := newCyberBlockTestCtx(nil, `{"input":"different"}`)
	c4, b4 := newCyberBlockTestCtx(nil, `{"input":[{"type":"input_text","text":"roleless user input"}]}`)

	key := CyberPolicyBlockKey(101, c1, ContentModerationProtocolOpenAIResponses, b1)
	require.Equal(t, key, CyberPolicyBlockKey(101, c2, ContentModerationProtocolOpenAIResponses, b2))
	require.NotEqual(t, key, CyberPolicyBlockKey(202, c2, ContentModerationProtocolOpenAIResponses, b2))
	require.NotEqual(t, key, CyberPolicyBlockKey(101, c3, ContentModerationProtocolOpenAIResponses, b3))
	require.NotEmpty(t, CyberPolicyBlockKey(101, c4, ContentModerationProtocolOpenAIResponses, b4))
}

func TestCyberPolicyBlockKeyExplicitSessionTakesPrecedence(t *testing.T) {
	c1, b1 := newCyberBlockTestCtx(map[string]string{"session-id": "stable-session"}, `{"input":"first message"}`)
	c2, b2 := newCyberBlockTestCtx(map[string]string{"session-id": "stable-session"}, `{"input":"second message"}`)
	require.Equal(t,
		CyberSessionBlockKey(101, c1, b1),
		CyberPolicyBlockKey(101, c1, ContentModerationProtocolOpenAIResponses, b1),
	)
	require.Equal(t,
		CyberPolicyBlockKey(101, c1, ContentModerationProtocolOpenAIResponses, b1),
		CyberPolicyBlockKey(101, c2, ContentModerationProtocolOpenAIResponses, b2),
	)
}

func TestCyberPolicyBlockKeyWithoutUserTextFailsOpen(t *testing.T) {
	tests := []struct {
		protocol string
		body     string
	}{
		{ContentModerationProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","output":"result"}]}`},
		{ContentModerationProtocolOpenAIResponses, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]}`},
		{ContentModerationProtocolOpenAIChat, `{"messages":[{"role":"tool","content":"result"}]}`},
		{ContentModerationProtocolOpenAIChat, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`},
		{ContentModerationProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":"result"}]}]}`},
		{ContentModerationProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/image.png"}}]}]}`},
		{ContentModerationProtocolOpenAIResponses, `not-json`},
	}
	for _, tt := range tests {
		c, body := newCyberBlockTestCtx(nil, tt.body)
		require.Empty(t, CyberPolicyBlockKey(101, c, tt.protocol, body))
	}
}

// --- fakes ---

type fakeCyberBlockStore struct {
	blocked      map[string]bool
	lastWriteTTL time.Duration
	lastWriteErr error
}

var _ CyberSessionBlockStore = (*fakeCyberBlockStore)(nil)

func (f *fakeCyberBlockStore) SetCyberSessionBlocked(ctx context.Context, key string, ttl time.Duration) error {
	if f.blocked == nil {
		f.blocked = map[string]bool{}
	}
	f.blocked[key] = true
	f.lastWriteTTL = ttl
	f.lastWriteErr = ctx.Err()
	return nil
}

func (f *fakeCyberBlockStore) IsCyberSessionBlocked(_ context.Context, key string) (bool, error) {
	return f.blocked[key], nil
}

// fakeSettingRepo is a minimal SettingRepository stub for unit tests.
// Only GetValue is exercised by GetCyberSessionBlockRuntime; all other methods
// panic so accidental calls are caught immediately.
type fakeSettingRepo struct {
	vals map[string]string
}

func (r *fakeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.vals[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}
func (r *fakeSettingRepo) Get(_ context.Context, _ string) (*Setting, error) {
	panic("fakeSettingRepo.Get not implemented")
}
func (r *fakeSettingRepo) Set(_ context.Context, _, _ string) error {
	panic("fakeSettingRepo.Set not implemented")
}
func (r *fakeSettingRepo) GetMultiple(_ context.Context, _ []string) (map[string]string, error) {
	panic("fakeSettingRepo.GetMultiple not implemented")
}
func (r *fakeSettingRepo) SetMultiple(_ context.Context, _ map[string]string) error {
	panic("fakeSettingRepo.SetMultiple not implemented")
}
func (r *fakeSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	panic("fakeSettingRepo.GetAll not implemented")
}
func (r *fakeSettingRepo) Delete(_ context.Context, _ string) error {
	panic("fakeSettingRepo.Delete not implemented")
}

var _ SettingRepository = (*fakeSettingRepo)(nil)

// comboCacheAndStore implements both GatewayCache (no-op stubs) and
// CyberSessionBlockStore (delegates to fakeCyberBlockStore) so it can be
// injected as s.cache and successfully type-asserted to CyberSessionBlockStore.
type comboCacheAndStore struct {
	store              fakeCyberBlockStore
	keywordStore       fakeCyberBlockStore
	keywordClaimCtxErr error
}

var _ GatewayCache = (*comboCacheAndStore)(nil)
var _ CyberSessionBlockStore = (*comboCacheAndStore)(nil)
var _ KeywordSessionBlockStore = (*comboCacheAndStore)(nil)

func (c *comboCacheAndStore) GetSessionAccountID(_ context.Context, _ int64, _ string) (int64, error) {
	return 0, errors.New("stub")
}
func (c *comboCacheAndStore) SetSessionAccountID(_ context.Context, _ int64, _ string, _ int64, _ time.Duration) error {
	return nil
}
func (c *comboCacheAndStore) RefreshSessionTTL(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}
func (c *comboCacheAndStore) DeleteSessionAccountID(_ context.Context, _ int64, _ string) error {
	return nil
}
func (c *comboCacheAndStore) SetCyberSessionBlocked(ctx context.Context, key string, ttl time.Duration) error {
	return c.store.SetCyberSessionBlocked(ctx, key, ttl)
}
func (c *comboCacheAndStore) IsCyberSessionBlocked(ctx context.Context, key string) (bool, error) {
	return c.store.IsCyberSessionBlocked(ctx, key)
}
func (c *comboCacheAndStore) ClaimKeywordSessionBlocked(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	c.keywordClaimCtxErr = ctx.Err()
	blocked, err := c.keywordStore.IsCyberSessionBlocked(ctx, key)
	if err != nil || blocked {
		return false, err
	}
	return true, c.keywordStore.SetCyberSessionBlocked(ctx, key, ttl)
}
func (c *comboCacheAndStore) IsKeywordSessionBlocked(ctx context.Context, key string) (bool, error) {
	return c.keywordStore.IsCyberSessionBlocked(ctx, key)
}

// --- tests ---

// TestIsCyberSessionBlocked_EmptyKeyAndNilService covers the fail-open paths:
// empty key, nil service, store missing → always false / no panic.
func TestIsCyberSessionBlocked_EmptyKeyAndNilService(t *testing.T) {
	var nilSvc *OpenAIGatewayService
	require.False(t, nilSvc.IsCyberSessionBlocked(context.Background(), "k"))
	require.NotPanics(t, func() { nilSvc.MarkCyberSessionBlocked(context.Background(), "k") })

	svc := &OpenAIGatewayService{}
	require.False(t, svc.IsCyberSessionBlocked(context.Background(), ""))
	require.False(t, svc.IsCyberSessionBlocked(context.Background(), "k"), "no store + no settings → fail-open false")
}

// TestCyberSessionBlock_RoundTrip exercises the type-assertion success path:
// mark a session blocked via a combo cache+store, then confirm IsCyberSessionBlocked
// returns true, and an unrelated key returns false.
func TestKeywordSessionBlock_RoundTripWhenCyberSwitchDisabled(t *testing.T) {
	settingSvc := &SettingService{
		settingRepo: &fakeSettingRepo{
			vals: map[string]string{
				SettingKeyCyberSessionBlockEnabled:    "false",
				SettingKeyCyberSessionBlockTTLSeconds: "60",
			},
		},
	}
	combo := &comboCacheAndStore{}
	svc := &OpenAIGatewayService{cache: combo, settingService: settingSvc}

	ctx := context.Background()
	const testKey = "keyword-deadbeef"
	require.False(t, svc.IsKeywordSessionBlocked(ctx, testKey))
	claimed, available := svc.ClaimKeywordSessionBlocked(ctx, testKey)
	require.True(t, available)
	require.True(t, claimed)
	require.True(t, svc.IsKeywordSessionBlocked(ctx, testKey))
	require.False(t, svc.IsCyberSessionBlocked(ctx, testKey), "keyword state must not leak into cyber namespace")
}

func TestKeywordSessionBlockClaimSurvivesCanceledRequestContext(t *testing.T) {
	settingSvc := &SettingService{
		settingRepo: &fakeSettingRepo{vals: map[string]string{
			SettingKeyCyberSessionBlockEnabled:    "false",
			SettingKeyCyberSessionBlockTTLSeconds: "60",
		}},
	}
	combo := &comboCacheAndStore{}
	svc := &OpenAIGatewayService{cache: combo, settingService: settingSvc}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	claimed, available := svc.ClaimKeywordSessionBlocked(ctx, "canceled-request")
	require.True(t, available)
	require.True(t, claimed)
	require.NoError(t, combo.keywordClaimCtxErr, "the store must receive a context detached from request cancellation")
	require.True(t, svc.IsKeywordSessionBlocked(context.Background(), "canceled-request"))
}

func TestCyberSessionBlockWriteUsesConfiguredTTLAndDetachedContext(t *testing.T) {
	settingSvc := &SettingService{
		settingRepo: &fakeSettingRepo{vals: map[string]string{
			SettingKeyCyberSessionBlockEnabled:    "true",
			SettingKeyCyberSessionBlockTTLSeconds: "36000",
		}},
	}
	combo := &comboCacheAndStore{}
	svc := &OpenAIGatewayService{cache: combo, settingService: settingSvc}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc.MarkCyberSessionBlocked(ctx, "message:v1:test")

	require.NoError(t, combo.store.lastWriteErr)
	require.Equal(t, 10*time.Hour, combo.store.lastWriteTTL)
	require.True(t, svc.IsCyberSessionBlocked(context.Background(), "message:v1:test"))
}

func TestCyberSessionBlock_RoundTrip(t *testing.T) {
	// SettingService with only settingRepo set — GetCyberSessionBlockRuntime needs
	// nothing else (cfg/proxyRepo/etc. are not touched by this code path).
	settingSvc := &SettingService{
		settingRepo: &fakeSettingRepo{
			vals: map[string]string{
				SettingKeyCyberSessionBlockEnabled:    "true",
				SettingKeyCyberSessionBlockTTLSeconds: "60",
			},
		},
	}

	combo := &comboCacheAndStore{}
	svc := &OpenAIGatewayService{
		cache:          combo,
		settingService: settingSvc,
	}

	ctx := context.Background()
	const testKey = "deadbeef1234"

	// Before marking: not blocked.
	require.False(t, svc.IsCyberSessionBlocked(ctx, testKey))

	// Mark as blocked.
	svc.MarkCyberSessionBlocked(ctx, testKey)

	// After marking: blocked.
	require.True(t, svc.IsCyberSessionBlocked(ctx, testKey))

	// Different key: still not blocked.
	require.False(t, svc.IsCyberSessionBlocked(ctx, "other-key"))
}
