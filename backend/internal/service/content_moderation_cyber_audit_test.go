package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildCyberPolicyRequestSnapshot_PreservesCompleteRequestExactly(t *testing.T) {
	raw := []byte(`{
  "model":"gpt-5-internal",
  "authorization":"Bearer retained-for-audit",
  "instructions":"system prompt",
  "input":[
    {"role":"user","content":"user input"},
    {"role":"assistant","content":"assistant output"},
    {"type":"function_call","arguments":"tool arguments"},
    {"type":"function_call_output","output":"tool output"},
    {"type":"input_image","image_url":"https://example.com/image.png?token=signed-secret"}
  ],
  "tools":[{"type":"function","name":"internal-tool"}],
  "seed":9007199254740993,
  "temperature":1.2300
}`)

	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIResponses, raw)

	require.Equal(t, string(raw), snapshot.Body)
	require.Equal(t, int64(len(raw)), snapshot.OriginalBytes)
	require.Equal(t, len(raw), snapshot.StoredBytes)
	require.False(t, snapshot.Truncated)
	require.Contains(t, snapshot.Body, "system prompt")
	require.Contains(t, snapshot.Body, "user input")
	require.Contains(t, snapshot.Body, "assistant output")
	require.Contains(t, snapshot.Body, "tool arguments")
	require.Contains(t, snapshot.Body, "tool output")
	require.Contains(t, snapshot.Body, "internal-tool")
	require.Contains(t, snapshot.Body, "signed-secret")
	require.Contains(t, snapshot.Body, "9007199254740993")
	require.Contains(t, snapshot.Body, "1.2300")
}

func TestBuildCyberPolicyRequestSnapshot_PreservesEveryProtocolBody(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		raw      string
	}{
		{
			name:     "OpenAI Chat Completions",
			protocol: ContentModerationProtocolOpenAIChat,
			raw:      `{"messages":[{"role":"system","content":"system"},{"role":"user","content":"user"},{"role":"assistant","content":"assistant"},{"role":"tool","content":"tool output"}]}`,
		},
		{
			name:     "OpenAI Responses",
			protocol: ContentModerationProtocolOpenAIResponses,
			raw:      `{"instructions":"system","input":[{"role":"user","content":"user"},{"type":"function_call_output","output":"tool output"}]}`,
		},
		{
			name:     "Responses WebSocket frame",
			protocol: ContentModerationProtocolOpenAIResponses,
			raw:      `{"type":"response.create","response":{"instructions":"system","input":[{"role":"user","content":"user"},{"type":"computer_call_output","output":"tool output"}]}}`,
		},
		{
			name:     "Anthropic Messages",
			protocol: ContentModerationProtocolAnthropicMessages,
			raw:      `{"system":"system","messages":[{"role":"user","content":[{"type":"text","text":"user"},{"type":"tool_result","content":"tool output"}]}]}`,
		},
		{
			name:     "Gemini",
			protocol: ContentModerationProtocolGemini,
			raw:      `{"systemInstruction":{"parts":[{"text":"system"}]},"contents":[{"role":"user","parts":[{"text":"user"},{"functionResponse":{"response":"tool output"}}]}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := buildCyberPolicyRequestSnapshot(test.protocol, []byte(test.raw))

			require.Equal(t, test.raw, snapshot.Body)
			require.Equal(t, int64(len(test.raw)), snapshot.OriginalBytes)
			require.Equal(t, len(test.raw), snapshot.StoredBytes)
			require.False(t, snapshot.Truncated)
			require.Contains(t, snapshot.Body, "tool output")
		})
	}
}

func TestBuildCyberPolicyRequestSnapshot_DoesNotTruncateLargeRequest(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("完整请求", 30000) + `"}]}`)
	require.Greater(t, len(raw), 64*1024)

	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIChat, raw)

	require.Equal(t, string(raw), snapshot.Body)
	require.Equal(t, int64(len(raw)), snapshot.OriginalBytes)
	require.Equal(t, len(raw), snapshot.StoredBytes)
	require.False(t, snapshot.Truncated)
}

func TestBuildCyberPolicyRequestSnapshot_PreservesInvalidJSONExactly(t *testing.T) {
	raw := []byte(`{"input":`)

	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIResponses, raw)

	require.Equal(t, string(raw), snapshot.Body)
	require.Equal(t, int64(len(raw)), snapshot.OriginalBytes)
	require.Equal(t, len(raw), snapshot.StoredBytes)
	require.False(t, snapshot.Truncated)
}

func TestBuildCyberPolicyRequestSnapshot_EmptyRequest(t *testing.T) {
	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIResponses, nil)

	require.Empty(t, snapshot.Body)
	require.Zero(t, snapshot.OriginalBytes)
	require.Zero(t, snapshot.StoredBytes)
	require.False(t, snapshot.Truncated)
}
