package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const cyberPolicyRequestSnapshotMaxBytes = 64 * 1024

const cyberPolicySnapshotTruncationMarker = "\n...<cyber request snapshot truncated>...\n"

type cyberPolicyRequestSnapshot struct {
	Body          string
	OriginalBytes int64
	StoredBytes   int
	Truncated     bool
}

func buildCyberPolicyRequestSnapshot(protocol string, raw []byte) cyberPolicyRequestSnapshot {
	result := cyberPolicyRequestSnapshot{OriginalBytes: int64(len(raw))}
	if len(raw) == 0 {
		return result
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		result.Body = fmt.Sprintf("<invalid JSON request omitted: %d bytes>", len(raw))
		result.StoredBytes = len(result.Body)
		return result
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		result.Body = fmt.Sprintf("<invalid JSON request omitted: %d bytes>", len(raw))
		result.StoredBytes = len(result.Body)
		return result
	}

	projected := projectCyberPolicyAuditRequest(protocol, value)
	redacted := redactCyberPolicyAuditValue(projected, "", nil, 0)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		result.Body = "<request snapshot redaction failed>"
		result.StoredBytes = len(result.Body)
		return result
	}

	result.Body, result.Truncated = truncateCyberPolicySnapshot(string(encoded), cyberPolicyRequestSnapshotMaxBytes)
	result.StoredBytes = len(result.Body)
	return result
}

func projectCyberPolicyAuditRequest(protocol string, value any) map[string]any {
	systemPrompt := make([]any, 0)
	userInput := make([]any, 0)
	root, _ := value.(map[string]any)
	if root == nil {
		return map[string]any{
			"system_prompt": systemPrompt,
			"user_input":    userInput,
		}
	}

	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case ContentModerationProtocolOpenAIChat:
		appendCyberPolicyAuditContent(&systemPrompt, root["instructions"])
		collectCyberPolicyMessageContent(root["messages"], &systemPrompt, &userInput)
	case ContentModerationProtocolAnthropicMessages:
		appendCyberPolicyAuditContent(&systemPrompt, root["system"])
		collectCyberPolicyMessageContent(root["messages"], &systemPrompt, &userInput)
	case ContentModerationProtocolOpenAIResponses:
		collectCyberPolicyResponsesRequest(root, &systemPrompt, &userInput)
	case ContentModerationProtocolGemini:
		collectCyberPolicyGeminiRequest(root, &systemPrompt, &userInput)
	case ContentModerationProtocolOpenAIImages:
		collectCyberPolicyMediaRequest(root, &userInput)
	default:
		appendCyberPolicyAuditContent(&systemPrompt, root["instructions"])
		appendCyberPolicyAuditContent(&systemPrompt, root["system"])
		collectCyberPolicyMessageContent(root["messages"], &systemPrompt, &userInput)
		collectCyberPolicyResponsesInput(root["input"], &systemPrompt, &userInput)
		collectCyberPolicyGeminiRequest(root, &systemPrompt, &userInput)
		collectCyberPolicyMediaRequest(root, &userInput)
	}

	return map[string]any{
		"system_prompt": systemPrompt,
		"user_input":    userInput,
	}
}

func collectCyberPolicyMessageContent(value any, systemPrompt, userInput *[]any) {
	messages, ok := value.([]any)
	if !ok {
		return
	}
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok || isCyberPolicyToolArtifact(message) {
			continue
		}
		var destination *[]any
		switch cyberPolicyObjectString(message, "role") {
		case "system", "developer":
			destination = systemPrompt
		case "user":
			destination = userInput
		default:
			continue
		}
		if content, exists := message["content"]; exists {
			appendCyberPolicyAuditContent(destination, content)
		} else if text, exists := message["text"]; exists {
			appendCyberPolicyAuditContent(destination, text)
		}
	}
}

func collectCyberPolicyResponsesRequest(root map[string]any, systemPrompt, userInput *[]any) {
	request := root
	if frameType := cyberPolicyObjectString(root, "type"); frameType != "" {
		if frameType != "response.create" {
			return
		}
		if response, ok := root["response"].(map[string]any); ok {
			request = response
		}
	}
	appendCyberPolicyAuditContent(systemPrompt, request["instructions"])
	collectCyberPolicyResponsesInput(request["input"], systemPrompt, userInput)
}

func collectCyberPolicyResponsesInput(value any, systemPrompt, userInput *[]any) {
	switch typed := value.(type) {
	case string:
		appendCyberPolicyAuditContent(userInput, typed)
	case []any:
		for _, item := range typed {
			collectCyberPolicyResponsesItem(item, systemPrompt, userInput)
		}
	case map[string]any:
		collectCyberPolicyResponsesItem(typed, systemPrompt, userInput)
	}
}

func collectCyberPolicyResponsesItem(value any, systemPrompt, userInput *[]any) {
	if text, ok := value.(string); ok {
		appendCyberPolicyAuditContent(userInput, text)
		return
	}
	item, ok := value.(map[string]any)
	if !ok || isCyberPolicyToolArtifact(item) {
		return
	}

	var destination *[]any
	switch cyberPolicyObjectString(item, "role") {
	case "system", "developer":
		destination = systemPrompt
	case "user":
		destination = userInput
	case "assistant", "model", "tool", "function":
		return
	default:
		if !isCyberPolicyDirectUserInputType(cyberPolicyObjectString(item, "type")) {
			return
		}
		destination = userInput
	}

	if content, exists := item["content"]; exists {
		appendCyberPolicyAuditContent(destination, content)
		return
	}
	if text, exists := item["text"]; exists {
		appendCyberPolicyAuditContent(destination, text)
		return
	}
	appendCyberPolicyAuditContent(destination, item)
}

func collectCyberPolicyGeminiRequest(root map[string]any, systemPrompt, userInput *[]any) {
	appendCyberPolicyAuditContent(systemPrompt, root["systemInstruction"])
	appendCyberPolicyAuditContent(systemPrompt, root["system_instruction"])
	collectCyberPolicyGeminiContents(root["contents"], systemPrompt, userInput)
	collectCyberPolicyGeminiContents(root["content"], systemPrompt, userInput)
}

func collectCyberPolicyGeminiContents(value any, systemPrompt, userInput *[]any) {
	var contents []any
	switch typed := value.(type) {
	case []any:
		contents = typed
	case map[string]any:
		contents = []any{typed}
	default:
		return
	}
	for _, item := range contents {
		content, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var destination *[]any
		switch cyberPolicyObjectString(content, "role") {
		case "", "user":
			destination = userInput
		case "system", "developer":
			destination = systemPrompt
		default:
			continue
		}
		appendCyberPolicyAuditContent(destination, content["parts"])
	}
}

func collectCyberPolicyMediaRequest(root map[string]any, userInput *[]any) {
	for _, key := range []string{"prompt", "input_prompt", "text_prompt", "negative_prompt", "image", "images", "mask"} {
		appendCyberPolicyAuditContent(userInput, root[key])
	}
}

func appendCyberPolicyAuditContent(destination *[]any, value any) {
	if projected, ok := projectCyberPolicyContent(value); ok {
		*destination = append(*destination, projected)
	}
}

func projectCyberPolicyContent(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, false
		}
		return typed, true
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			if projected, ok := projectCyberPolicyContent(item); ok {
				out = append(out, projected)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case map[string]any:
		if isCyberPolicyToolArtifact(typed) {
			return nil, false
		}
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if projected, ok := projectCyberPolicyContent(item); ok {
				out[key] = projected
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case nil:
		return nil, false
	default:
		return value, true
	}
}

func isCyberPolicyToolArtifact(value map[string]any) bool {
	switch cyberPolicyObjectString(value, "role") {
	case "assistant", "model", "tool", "function":
		return true
	}
	for _, key := range []string{
		"tool_result", "toolResult", "tool_output", "toolOutput",
		"function_response", "functionResponse",
		"tool_call_id", "toolCallId", "tool_use_id", "toolUseId",
	} {
		if _, exists := value[key]; exists {
			return true
		}
	}
	typ := strings.NewReplacer("-", "_", ".", "_").Replace(cyberPolicyObjectString(value, "type"))
	switch typ {
	case "tool_result", "tool_output", "tool_use",
		"function_result", "function_response", "function_call", "function_call_output",
		"computer_call", "computer_call_output",
		"local_shell_call", "local_shell_call_output",
		"custom_tool_call", "custom_tool_call_output",
		"mcp_call", "mcp_call_output", "mcp_list_tools",
		"web_search_call", "file_search_call", "code_interpreter_call",
		"output_text", "output_audio", "reasoning":
		return true
	default:
		return strings.HasSuffix(typ, "_call_output") ||
			strings.HasSuffix(typ, "_tool_output") ||
			strings.HasSuffix(typ, "_tool_result") ||
			strings.HasSuffix(typ, "_search_output")
	}
}

func isCyberPolicyDirectUserInputType(typ string) bool {
	switch strings.NewReplacer("-", "_", ".", "_").Replace(typ) {
	case "", "message", "text", "input_text", "input_image", "image", "image_url",
		"input_audio", "audio", "input_file", "file":
		return true
	default:
		return false
	}
}

func cyberPolicyObjectString(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return strings.ToLower(strings.TrimSpace(text))
}

func redactCyberPolicyAuditValue(value any, key string, parent map[string]any, depth int) any {
	if depth > auditRedactMaxDepth {
		return "<depth limit exceeded>"
	}
	if key != "" && isAuditSensitiveBodyKey(key) {
		return auditRedactedPlaceholder
	}

	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = redactCyberPolicyAuditValue(child, childKey, typed, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactCyberPolicyAuditValue(child, "", parent, depth+1)
		}
		return out
	case string:
		if isCyberPolicyInlineBinary(key, typed, parent) {
			return cyberPolicyInlineBinaryPlaceholder(typed, cyberPolicyInlineMediaType(parent))
		}
		return redactCyberPolicyAuditString(typed)
	default:
		return value
	}
}

func isCyberPolicyInlineBinary(key, value string, parent map[string]any) bool {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") && strings.Contains(strings.ToLower(trimmed[:min(len(trimmed), 256)]), ";base64,") {
		return true
	}
	normalizedKey := auditNormalizeBodyKey(key)
	if normalizedKey == "base64" || normalizedKey == "filedata" || normalizedKey == "imagedata" || normalizedKey == "audiodata" {
		return true
	}
	if normalizedKey != "data" || parent == nil {
		return false
	}
	for _, candidate := range []string{"type", "media_type", "mediaType", "mime_type", "mimeType"} {
		if raw, ok := parent[candidate].(string); ok {
			kind := strings.ToLower(strings.TrimSpace(raw))
			if strings.Contains(kind, "image") || strings.Contains(kind, "audio") || strings.Contains(kind, "file") || strings.Contains(kind, "octet-stream") {
				return true
			}
		}
	}
	if format, ok := parent["format"].(string); ok && strings.TrimSpace(format) != "" {
		return true
	}
	return false
}

func cyberPolicyInlineMediaType(parent map[string]any) string {
	for _, candidate := range []string{"media_type", "mediaType", "mime_type", "mimeType"} {
		if raw, ok := parent[candidate].(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw)
		}
	}
	return "unknown"
}

func cyberPolicyInlineBinaryPlaceholder(value string, mediaType string) string {
	payload := strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(payload), "data:") {
		if comma := strings.IndexByte(payload, ','); comma >= 0 {
			header := payload[len("data:"):comma]
			if semi := strings.IndexByte(header, ';'); semi >= 0 {
				header = header[:semi]
			}
			if strings.TrimSpace(header) != "" {
				mediaType = strings.TrimSpace(header)
			}
			payload = payload[comma+1:]
		}
	}
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("<inline binary omitted: media_type=%s encoded_bytes=%d sha256=%s>", mediaType, len(payload), hex.EncodeToString(digest[:]))
}

func redactCyberPolicyAuditString(value string) string {
	if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		query := parsed.Query()
		queryChanged := false
		for key, values := range query {
			if isCyberPolicySensitiveURLQueryKey(key) {
				query.Set(key, auditRedactedPlaceholder)
				queryChanged = true
				continue
			}
			for i, item := range values {
				redacted := redactCyberPolicyAuditCredentialText(item)
				if redacted != item {
					values[i] = redacted
					queryChanged = true
				}
			}
		}
		if parsed.User != nil {
			parsed.User = url.User(auditRedactedPlaceholder)
		}
		if queryChanged {
			parsed.RawQuery = query.Encode()
		}
		if redacted := redactCyberPolicyAuditCredentialText(parsed.Path); redacted != parsed.Path {
			parsed.Path = redacted
			parsed.RawPath = ""
		}
		if redacted := redactCyberPolicyAuditCredentialText(parsed.Fragment); redacted != parsed.Fragment {
			parsed.Fragment = redacted
			parsed.RawFragment = ""
		}
		return parsed.String()
	}
	return redactCyberPolicyAuditCredentialText(value)
}

func redactCyberPolicyAuditCredentialText(value string) string {
	out := logredact.RedactText(value, "api_key", "apikey", "access_key", "session", "cookie", "authorization", "private_key")
	for idx, pattern := range contentModerationSecretPatterns {
		if idx == 0 || idx > 4 {
			continue
		}
		switch idx {
		case 1:
			out = pattern.ReplaceAllString(out, `${1}${2}[redacted]`)
		case 2:
			out = pattern.ReplaceAllString(out, `${1}[redacted]`)
		default:
			out = pattern.ReplaceAllString(out, `[redacted]`)
		}
	}
	out = sanitizeErrorMessage(out)
	return out
}

func isCyberPolicySensitiveURLQueryKey(key string) bool {
	if isAuditSensitiveBodyKey(key) {
		return true
	}
	normalized := auditNormalizeBodyKey(key)
	for _, marker := range []string{"signature", "credential", "keypairid", "signedheaders"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func truncateCyberPolicySnapshot(value string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value, false
	}
	available := maxBytes - len(cyberPolicySnapshotTruncationMarker)
	if available <= 0 {
		return cyberPolicySnapshotTruncationMarker[:maxBytes], true
	}
	headBytes := available / 2
	tailBytes := available - headBytes
	head := validUTF8Prefix(value, headBytes)
	tail := validUTF8Suffix(value, tailBytes)
	return head + cyberPolicySnapshotTruncationMarker + tail, true
}

func validUTF8Prefix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func validUTF8Suffix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}
