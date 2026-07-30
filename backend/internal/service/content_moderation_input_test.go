package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 当数组末尾不是用户消息时（典型场景：Agent 工具循环结束于 tool/assistant），
// 应直接跳过审计——不再回溯查找历史中的某条用户消息。

func TestExtractContentModerationInput_AnthropicAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"调用一下天气工具"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"weather","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"晴 25 度"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_AnthropicFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q1", input.Text)
}

func TestExtractContentModerationInput_AnthropicMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_AnthropicStreamResendExtractsResend(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"原问题"},
			{"role":"assistant","content":"部分回答……"},
			{"role":"user","content":"重发"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "重发", input.Text)
}

func TestExtractContentModerationInput_OpenAIChatAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"system","content":"sys"},
			{"role":"user","content":"列出我的订单"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"orders","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"[]"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_OpenAIChatMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_GeminiAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"查询天气"}]},
			{"role":"model","parts":[{"functionCall":{"name":"weather","args":{}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"weather","response":{"temp":25}}}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_GeminiFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"你好"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "你好", input.Text)
}

func TestExtractContentModerationInput_GeminiMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"Q1"}]},
			{"role":"model","parts":[{"text":"A1"}]},
			{"role":"user","parts":[{"text":"Q2"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_ResponsesAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"运行测试"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"all passed"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_ResponsesLastUserMessageExtracted(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "latest", input.Text)
}

func TestExtractContentModerationInput_ResponsesLastIsAssistantSkipped(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractOpenAIKeywordText_FieldSelection(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		body     string
		want     string
		excluded []string
	}{
		{
			name:     "chat history declared and called tools",
			protocol: ContentModerationProtocolOpenAIChat,
			body: `{
				"messages":[
					{"role":"system","content":"system text"},
					{"role":"developer","content":"developer text"},
					{"role":"user","content":"first user"},
					{"role":"assistant","content":"assistant text","tool_calls":[{"function":{"name":"called_function","arguments":"arguments text"}}]},
					{"role":"tool","content":"tool output"},
					{"role":"user","content":[{"type":"text","text":"second user"},{"type":"image_url","image_url":{"url":"https://metadata.invalid/image"}}]}
				],
				"tools":[{"type":"function","function":{"name":"declared_function","description":"description text","parameters":{"const":"schema text"}}}]
			}`,
			want:     "system text developer text first user called_function second user declared_function",
			excluded: []string{"assistant text", "arguments text", "tool output", "metadata.invalid", "description text", "schema text"},
		},
		{
			name:     "responses instructions custom and MCP tools",
			protocol: ContentModerationProtocolOpenAIResponses,
			body: `{
				"instructions":"instructions text",
				"input":[
					"historical string",
					{"type":"message","role":"system","content":"system text"},
					{"type":"message","role":"developer","content":"developer text"},
					{"type":"message","role":"user","content":"user text"},
					{"type":"message","role":"assistant","content":"assistant text"},
					{"type":"custom_tool_call","name":"custom_call","input":"custom input"},
					{"type":"mcp_tool_call","name":"mcp_call","arguments":{"value":"arguments text"}},
					{"type":"function_call_output","output":"tool output"},
					{"type":"mcp_list_tools","tools":[{"name":"listed_tool","description":"description text"}]}
				],
				"tools":[
					{"type":"function","name":"declared_tool","description":"declared description"},
					{"type":"mcp","allowed_tools":{"tool_names":["allowed_tool"]},"headers":{"Authorization":"header text"},"server_url":"https://server.invalid"}
				]
			}`,
			want:     "instructions text historical string system text developer text user text custom_call mcp_call listed_tool declared_tool allowed_tool",
			excluded: []string{"assistant text", "custom input", "arguments text", "tool output", "description text", "declared description", "header text", "server.invalid"},
		},
		{
			name:     "codex memory instructions history and call name",
			protocol: ContentModerationProtocolOpenAICodexMemory,
			body: `{
				"instructions":"codex instructions",
				"traces":[{"items":[
					{"type":"message","role":"system","content":"system text"},
					{"type":"message","role":"developer","content":"developer text"},
					{"type":"message","role":"user","content":"user text"},
					{"type":"function_call","name":"codex_tool","arguments":"arguments text"},
					{"type":"function_call_output","output":"tool output"}
				]}],
				"tools":[{"type":"custom","name":"declared_codex_tool","description":"description text"}]
			}`,
			want:     "codex instructions system text developer text user text codex_tool declared_codex_tool",
			excluded: []string{"arguments text", "tool output", "description text"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOpenAIKeywordText(tt.protocol, []byte(tt.body))
			require.Equal(t, tt.want, got)
			for _, excluded := range tt.excluded {
				require.NotContains(t, got, excluded)
			}
		})
	}
}
