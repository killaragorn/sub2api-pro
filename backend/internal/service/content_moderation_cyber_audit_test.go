package service

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestBuildCyberPolicyRequestSnapshot_ProjectsAndRedactsSystemAndUserContent(t *testing.T) {
	raw := []byte(`{
  "model":"gpt-5-internal",
  "authorization":"Bearer should-not-survive",
  "instructions":"system api_key=sk-proj-system1234567890 and keep this system prompt",
  "input":[
    {"role":"user","content":[
      {"type":"input_text","text":"inspect xai-user1234567890 and keep this user input"},
      {"type":"input_image","media_type":"image/png","data":"aGVsbG8="},
      {"type":"input_image","image_url":"https://example.com/image.png?token=signed-secret&X-Amz-Signature=aws-signature&width=200"},
      {"type":"input_audio","data":"YXVkaW8=","format":"wav"}
    ]},
    {"type":"function_call_output","call_id":"call-1","output":"tool output must not survive"}
  ],
  "tools":[{"type":"function","name":"internal-tool"}],
  "temperature":1.2300
}`)

	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIResponses, raw)

	require.False(t, snapshot.Truncated)
	require.Equal(t, int64(len(raw)), snapshot.OriginalBytes)
	require.Equal(t, len(snapshot.Body), snapshot.StoredBytes)
	require.True(t, json.Valid([]byte(snapshot.Body)))
	var projected map[string]any
	require.NoError(t, json.Unmarshal([]byte(snapshot.Body), &projected))
	require.Len(t, projected, 2)
	require.Contains(t, projected, "system_prompt")
	require.Contains(t, projected, "user_input")
	require.NotContains(t, snapshot.Body, "gpt-5-internal")
	require.NotContains(t, snapshot.Body, "should-not-survive")
	require.NotContains(t, snapshot.Body, "internal-tool")
	require.NotContains(t, snapshot.Body, "temperature")
	require.NotContains(t, snapshot.Body, "tool output must not survive")
	require.NotContains(t, snapshot.Body, "sk-proj-system1234567890")
	require.NotContains(t, snapshot.Body, "xai-user1234567890")
	require.NotContains(t, snapshot.Body, "signed-secret")
	require.NotContains(t, snapshot.Body, "aws-signature")
	require.NotContains(t, snapshot.Body, "aGVsbG8=")
	require.NotContains(t, snapshot.Body, "YXVkaW8=")
	require.Contains(t, snapshot.Body, "keep this system prompt")
	require.Contains(t, snapshot.Body, "keep this user input")
	require.Contains(t, snapshot.Body, "inline binary omitted")
	require.Contains(t, snapshot.Body, "sha256=")
	require.Contains(t, snapshot.Body, "width=200")
}

func TestBuildCyberPolicyRequestSnapshot_ExcludesAssistantAndToolOutputByProtocol(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		raw      string
		keep     []string
		drop     []string
	}{
		{
			name:     "OpenAI Chat Completions",
			protocol: ContentModerationProtocolOpenAIChat,
			raw: `{"messages":[
				{"role":"system","content":"CHAT_SYSTEM"},
				{"role":"user","content":"CHAT_USER_FIRST"},
				{"role":"assistant","content":"CHAT_ASSISTANT_OUTPUT","tool_calls":[{"function":{"arguments":"CHAT_TOOL_ARGUMENTS"}}]},
				{"role":"tool","content":"CHAT_TOOL_OUTPUT"},
				{"role":"function","content":"CHAT_FUNCTION_OUTPUT"},
				{"role":"user","tool_call_id":"legacy-call","content":"CHAT_DISGUISED_TOOL_OUTPUT"},
				{"role":"user","content":[{"type":"text","text":"CHAT_USER_LAST"}]}
			]}`,
			keep: []string{"CHAT_SYSTEM", "CHAT_USER_FIRST", "CHAT_USER_LAST"},
			drop: []string{"CHAT_ASSISTANT_OUTPUT", "CHAT_TOOL_ARGUMENTS", "CHAT_TOOL_OUTPUT", "CHAT_FUNCTION_OUTPUT", "CHAT_DISGUISED_TOOL_OUTPUT"},
		},
		{
			name:     "OpenAI Responses",
			protocol: ContentModerationProtocolOpenAIResponses,
			raw: `{"instructions":"RESPONSES_SYSTEM","input":[
				{"role":"user","content":[{"type":"input_text","text":"RESPONSES_USER"}]},
				{"type":"function_call","arguments":"RESPONSES_TOOL_ARGUMENTS"},
				{"type":"function_call_output","output":"RESPONSES_TOOL_OUTPUT"},
				{"type":"tool_search_output","output":"RESPONSES_SEARCH_OUTPUT"},
				{"role":"assistant","content":[{"type":"output_text","text":"RESPONSES_ASSISTANT_OUTPUT"}]},
				{"type":"input_text","text":"RESPONSES_USER_DIRECT"}
			]}`,
			keep: []string{"RESPONSES_SYSTEM", "RESPONSES_USER", "RESPONSES_USER_DIRECT"},
			drop: []string{"RESPONSES_TOOL_ARGUMENTS", "RESPONSES_TOOL_OUTPUT", "RESPONSES_SEARCH_OUTPUT", "RESPONSES_ASSISTANT_OUTPUT"},
		},
		{
			name:     "Responses WebSocket frame",
			protocol: ContentModerationProtocolOpenAIResponses,
			raw: `{"type":"response.create","response":{"instructions":"WS_SYSTEM","input":[
				{"role":"user","content":"WS_USER"},
				{"type":"computer_call_output","output":"WS_TOOL_OUTPUT"}
			]}}`,
			keep: []string{"WS_SYSTEM", "WS_USER"},
			drop: []string{"WS_TOOL_OUTPUT"},
		},
		{
			name:     "Anthropic Messages",
			protocol: ContentModerationProtocolAnthropicMessages,
			raw: `{"system":[{"type":"text","text":"ANTHROPIC_SYSTEM"}],"messages":[
				{"role":"user","content":"ANTHROPIC_USER"},
				{"role":"assistant","content":[{"type":"tool_use","input":"ANTHROPIC_TOOL_ARGUMENTS"}]},
				{"role":"user","content":[
					{"type":"tool_result","content":"ANTHROPIC_TOOL_OUTPUT"},
					{"type":"web_search_tool_result","content":"ANTHROPIC_SEARCH_OUTPUT"},
					{"type":"text","text":"ANTHROPIC_USER_FOLLOWUP"}
				]}
			]}`,
			keep: []string{"ANTHROPIC_SYSTEM", "ANTHROPIC_USER", "ANTHROPIC_USER_FOLLOWUP"},
			drop: []string{"ANTHROPIC_TOOL_ARGUMENTS", "ANTHROPIC_TOOL_OUTPUT", "ANTHROPIC_SEARCH_OUTPUT"},
		},
		{
			name:     "Gemini",
			protocol: ContentModerationProtocolGemini,
			raw: `{"systemInstruction":{"parts":[{"text":"GEMINI_SYSTEM"}]},"contents":[
				{"role":"user","parts":[{"text":"GEMINI_USER"}]},
				{"role":"model","parts":[{"functionCall":{"args":"GEMINI_TOOL_ARGUMENTS"}}]},
				{"role":"user","parts":[
					{"functionResponse":{"response":"GEMINI_TOOL_OUTPUT"}},
					{"text":"GEMINI_USER_FOLLOWUP"}
				]}
			]}`,
			keep: []string{"GEMINI_SYSTEM", "GEMINI_USER", "GEMINI_USER_FOLLOWUP"},
			drop: []string{"GEMINI_TOOL_ARGUMENTS", "GEMINI_TOOL_OUTPUT"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := buildCyberPolicyRequestSnapshot(test.protocol, []byte(test.raw))

			require.True(t, json.Valid([]byte(snapshot.Body)))
			for _, expected := range test.keep {
				require.Contains(t, snapshot.Body, expected)
			}
			for _, forbidden := range test.drop {
				require.NotContains(t, snapshot.Body, forbidden)
			}
		})
	}
}

func TestBuildCyberPolicyRequestSnapshot_RedactsCredentialInURLPath(t *testing.T) {
	raw := []byte(`{"input":"https://example.com/files/sk-proj-1234567890abcdef/result"}`)

	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIResponses, raw)

	require.NotContains(t, snapshot.Body, "sk-proj-1234567890abcdef")
	require.Contains(t, snapshot.Body, "%5Bredacted%5D")
}

func TestBuildCyberPolicyRequestSnapshot_TruncatesWithUTF8HeadAndTail(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": strings.Repeat("头", 30000)},
			map[string]any{"role": "user", "content": strings.Repeat("尾", 30000)},
		},
	})
	require.NoError(t, err)

	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIChat, raw)

	require.True(t, snapshot.Truncated)
	require.LessOrEqual(t, snapshot.StoredBytes, cyberPolicyRequestSnapshotMaxBytes)
	require.True(t, utf8.ValidString(snapshot.Body))
	require.Contains(t, snapshot.Body, cyberPolicySnapshotTruncationMarker)
	require.Contains(t, snapshot.Body, "头")
	require.Contains(t, snapshot.Body, "尾")
}

func TestBuildCyberPolicyRequestSnapshot_OmitsRequestConfiguration(t *testing.T) {
	raw := []byte(`{"seed":9007199254740993,"temperature":1.2300,"input":"audit"}`)

	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIResponses, raw)

	require.False(t, snapshot.Truncated)
	require.Contains(t, snapshot.Body, `"user_input":["audit"]`)
	require.NotContains(t, snapshot.Body, "seed")
	require.NotContains(t, snapshot.Body, "temperature")
}

func TestBuildCyberPolicyRequestSnapshot_InvalidJSONUsesMetadataPlaceholder(t *testing.T) {
	raw := []byte(`{"input":`)
	snapshot := buildCyberPolicyRequestSnapshot(ContentModerationProtocolOpenAIResponses, raw)

	require.Equal(t, int64(len(raw)), snapshot.OriginalBytes)
	require.Contains(t, snapshot.Body, "invalid JSON request omitted")
	require.NotContains(t, snapshot.Body, string(raw))
}
