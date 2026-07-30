package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/tidwall/gjson"
)

func ExtractContentModerationText(protocol string, body []byte) string {
	return ExtractContentModerationInput(protocol, body).Text
}

func ExtractContentModerationInput(protocol string, body []byte) ContentModerationInput {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ContentModerationInput{}
	}
	var parts []string
	var images []string
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		collectLastAnthropicUserMessage(gjson.GetBytes(body, "messages"), &parts, &images)
	case ContentModerationProtocolOpenAIChat:
		collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
	case ContentModerationProtocolOpenAIResponses:
		collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
	case ContentModerationProtocolGemini:
		collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	case ContentModerationProtocolOpenAIImages:
		addModerationText(&parts, gjson.GetBytes(body, "prompt").String())
		collectContentValue(gjson.GetBytes(body, "images"), &parts, &images)
	case ContentModerationProtocolOpenAICodexMemory:
		collectCodexMemoryTraceInput(gjson.GetBytes(body, "traces"), &parts, &images)
	default:
		collectLastResponsesInput(gjson.GetBytes(body, "input"), &parts, &images)
		collectLastRoleMessage(gjson.GetBytes(body, "messages"), "user", &parts, &images)
		collectLastGeminiContent(gjson.GetBytes(body, "contents"), &parts, &images)
	}
	out := ContentModerationInput{
		Text:   normalizeContentModerationText(strings.Join(parts, "\n")),
		Images: normalizeModerationImages(images),
	}
	out.Normalize()
	return out
}

func extractOpenAIKeywordText(protocol string, body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	var keywordParts []string
	switch protocol {
	case ContentModerationProtocolOpenAIChat:
		collectOpenAIChatKeywordText(gjson.GetBytes(body, "messages"), &keywordParts)
		collectOpenAIToolNameTree(gjson.GetBytes(body, "tools"), &keywordParts)
	case ContentModerationProtocolOpenAIResponses:
		addModerationText(&keywordParts, gjson.GetBytes(body, "instructions").String())
		collectOpenAIResponsesKeywordText(gjson.GetBytes(body, "input"), &keywordParts)
		collectOpenAIToolNameTree(gjson.GetBytes(body, "tools"), &keywordParts)
	case ContentModerationProtocolOpenAICodexMemory:
		addModerationText(&keywordParts, gjson.GetBytes(body, "instructions").String())
		collectOpenAICodexMemoryKeywordText(gjson.GetBytes(body, "traces"), &keywordParts)
		collectOpenAIToolNameTree(gjson.GetBytes(body, "tools"), &keywordParts)
	}
	return normalizeContentModerationText(strings.Join(keywordParts, "\n"))
}

func usesExtendedOpenAIKeywordText(protocol string) bool {
	switch protocol {
	case ContentModerationProtocolOpenAIChat,
		ContentModerationProtocolOpenAIResponses,
		ContentModerationProtocolOpenAICodexMemory:
		return true
	default:
		return false
	}
}

func collectCodexMemoryTraceInput(traces gjson.Result, parts *[]string, images *[]string) {
	if !traces.IsArray() {
		return
	}
	traces.ForEach(func(_, trace gjson.Result) bool {
		items := trace.Get("items")
		if !items.IsArray() {
			return true
		}
		items.ForEach(func(_, item gjson.Result) bool {
			role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
			if role != "" && role != "user" {
				return true
			}
			collectContentValue(item.Get("content"), parts, images)
			if item.Get("type").String() == "input_text" || item.Get("text").Exists() {
				collectContentValue(item, parts, images)
			}
			return true
		})
		return true
	})
}

func collectOpenAICodexMemoryKeywordText(traces gjson.Result, parts *[]string) {
	if !traces.IsArray() {
		return
	}
	traces.ForEach(func(_, trace gjson.Result) bool {
		items := trace.Get("items")
		if !items.IsArray() {
			return true
		}
		items.ForEach(func(_, item gjson.Result) bool {
			collectOpenAIResponsesKeywordItem(item, parts)
			return true
		})
		return true
	})
}

func collectOpenAIChatKeywordText(messages gjson.Result, parts *[]string) {
	if !messages.IsArray() {
		return
	}
	messages.ForEach(func(_, message gjson.Result) bool {
		role := strings.ToLower(strings.TrimSpace(message.Get("role").String()))
		if role == "user" || role == "system" || role == "developer" {
			collectOpenAIKeywordContentText(message.Get("content"), parts)
		}
		toolCalls := message.Get("tool_calls")
		if toolCalls.IsArray() {
			toolCalls.ForEach(func(_, call gjson.Result) bool {
				addModerationText(parts, call.Get("function.name").String())
				addModerationText(parts, call.Get("name").String())
				return true
			})
		}
		if functionCall := message.Get("function_call"); functionCall.IsObject() {
			addModerationText(parts, functionCall.Get("name").String())
		}
		return true
	})
}

func collectLastRoleMessage(messages gjson.Result, role string, parts *[]string, images *[]string) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	if strings.ToLower(strings.TrimSpace(last.Get("role").String())) != role {
		return
	}
	var candidate []string
	var candidateImages []string
	collectContentValue(last.Get("content"), &candidate, &candidateImages)
	if normalizeContentModerationText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func collectLastAnthropicUserMessage(messages gjson.Result, parts *[]string, images *[]string) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	if strings.ToLower(strings.TrimSpace(last.Get("role").String())) != "user" {
		return
	}
	var candidate []string
	var candidateImages []string
	collectAnthropicUserContentValue(last.Get("content"), &candidate, &candidateImages)
	if normalizeContentModerationText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func collectAnthropicUserContentValue(value gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		if !isAnthropicSystemReminderText(value.String()) {
			addModerationText(parts, value.String())
		}
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collectAnthropicUserContentValue(item, parts, images)
			return true
		})
	case value.IsObject():
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		switch typ {
		case "", "text", "input_text", "message":
			if value.Get("text").Exists() && !isAnthropicSystemReminderText(value.Get("text").String()) {
				addModerationText(parts, value.Get("text").String())
			}
			if value.Get("content").Exists() {
				collectAnthropicUserContentValue(value.Get("content"), parts, images)
			}
		case "image_url", "input_image", "image":
			collectContentValue(value, parts, images)
		}
	}
}

func isAnthropicSystemReminderText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "<system-reminder>")
}

func collectOpenAIResponsesKeywordText(input gjson.Result, parts *[]string) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		addModerationText(parts, input.String())
	case input.IsArray():
		input.ForEach(func(_, item gjson.Result) bool {
			collectOpenAIResponsesKeywordItem(item, parts)
			return true
		})
	case input.IsObject():
		collectOpenAIResponsesKeywordItem(input, parts)
	}
}

func collectLastResponsesInput(input gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		addModerationText(parts, input.String())
	case input.IsArray():
		array := input.Array()
		if len(array) == 0 {
			return
		}
		last := array[len(array)-1]
		if !isResponsesUserTextItem(last) {
			return
		}
		collectContentValue(last.Get("content"), parts, images)
		if last.Get("type").String() == "input_text" || last.Get("text").Exists() {
			collectContentValue(last, parts, images)
		}
	case input.IsObject():
		if isResponsesUserTextItem(input) {
			collectContentValue(input.Get("content"), parts, images)
			if input.Get("type").String() == "input_text" || input.Get("text").Exists() {
				collectContentValue(input, parts, images)
			}
		}
	}
}

func isResponsesUserTextItem(item gjson.Result) bool {
	role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
	if role == "user" {
		return responseItemHasModerationText(item)
	}
	if role != "" {
		return false
	}
	return responseItemHasModerationText(item)
}

func collectOpenAIResponsesKeywordItem(item gjson.Result, parts *[]string) {
	if item.Type == gjson.String {
		addModerationText(parts, item.String())
		return
	}
	typeName := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
	if role == "user" || role == "system" || role == "developer" ||
		(role == "" && (typeName == "" || typeName == "message" || typeName == "input_text")) {
		collectOpenAIKeywordContentText(item.Get("content"), parts)
		if typeName == "input_text" || item.Get("text").Exists() {
			collectOpenAIKeywordContentText(item, parts)
		}
	}
	collectOpenAIToolName(item, parts)
}

func collectOpenAIKeywordContentText(value gjson.Result, parts *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		addModerationText(parts, value.String())
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collectOpenAIKeywordContentText(item, parts)
			return true
		})
	case value.IsObject():
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		switch typ {
		case "", "text", "input_text", "message":
			if value.Get("text").Exists() {
				addModerationText(parts, value.Get("text").String())
			}
			if value.Get("content").Exists() {
				collectOpenAIKeywordContentText(value.Get("content"), parts)
			}
		}
	}
}

func collectOpenAIToolName(item gjson.Result, parts *[]string) {
	typeName := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	if strings.HasSuffix(typeName, "_call") || typeName == "mcp_approval_request" {
		addModerationText(parts, item.Get("name").String())
	}
	if typeName == "mcp_list_tools" || typeName == "additional_tools" {
		collectOpenAIToolNameTree(item.Get("tools"), parts)
	}
}

func collectOpenAIToolNameTree(value gjson.Result, parts *[]string) {
	switch {
	case value.Type == gjson.String:
		addModerationText(parts, value.String())
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collectOpenAIToolNameTree(item, parts)
			return true
		})
	case value.IsObject():
		addModerationText(parts, value.Get("name").String())
		addModerationText(parts, value.Get("function.name").String())
		collectOpenAIToolNameTree(value.Get("tools"), parts)
		collectOpenAIToolNameTree(value.Get("children"), parts)
		allowedTools := value.Get("allowed_tools")
		if allowedTools.IsArray() {
			collectOpenAIToolNameTree(allowedTools, parts)
		} else if allowedTools.IsObject() {
			collectOpenAIToolNameTree(allowedTools.Get("tool_names"), parts)
		}
	}
}

func responseItemHasModerationText(item gjson.Result) bool {
	var parts []string
	var images []string
	collectContentValue(item.Get("content"), &parts, &images)
	if item.Get("type").String() == "input_text" || item.Get("text").Exists() {
		collectContentValue(item, &parts, &images)
	}
	return normalizeContentModerationText(strings.Join(parts, "\n")) != "" || len(images) > 0
}

func collectLastGeminiContent(contents gjson.Result, parts *[]string, images *[]string) {
	if !contents.IsArray() {
		return
	}
	array := contents.Array()
	if len(array) == 0 {
		return
	}
	last := array[len(array)-1]
	role := strings.ToLower(strings.TrimSpace(last.Get("role").String()))
	if role != "" && role != "user" {
		return
	}
	var candidate []string
	var candidateImages []string
	if arr := last.Get("parts"); arr.IsArray() {
		arr.ForEach(func(_, part gjson.Result) bool {
			addModerationText(&candidate, part.Get("text").String())
			addGeminiModerationImage(&candidateImages, part)
			return true
		})
	}
	if normalizeContentModerationText(strings.Join(candidate, "\n")) == "" && len(candidateImages) == 0 {
		return
	}
	*parts = append(*parts, candidate...)
	*images = append(*images, candidateImages...)
}

func collectContentValue(value gjson.Result, parts *[]string, images *[]string) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		addModerationText(parts, value.String())
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collectContentValue(item, parts, images)
			return true
		})
	case value.IsObject():
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		addModerationImage(images, value.Get("image_url.url").String())
		addModerationImage(images, value.Get("image_url").String())
		addModerationImage(images, value.Get("url").String())
		addModerationImageData(images, value.Get("source.media_type").String(), value.Get("source.data").String())
		addModerationImageData(images, value.Get("source.mediaType").String(), value.Get("source.data").String())
		addModerationImageData(images, value.Get("media_type").String(), value.Get("data").String())
		addModerationImageData(images, value.Get("mime_type").String(), value.Get("data").String())
		addModerationImageData(images, value.Get("mimeType").String(), value.Get("data").String())
		addModerationImage(images, value.Get("source.data").String())
		addModerationImage(images, value.Get("data").String())
		addModerationImage(images, value.Get("base64").String())
		switch typ {
		case "", "text", "input_text", "message":
			if value.Get("text").Exists() {
				addModerationText(parts, value.Get("text").String())
			}
			if value.Get("content").Exists() {
				collectContentValue(value.Get("content"), parts, images)
			}
		case "image_url", "input_image", "image":
		}
	}
}

func addGeminiModerationImage(images *[]string, part gjson.Result) {
	if inlineData := part.Get("inline_data"); inlineData.IsObject() {
		mimeType := strings.TrimSpace(inlineData.Get("mime_type").String())
		data := strings.TrimSpace(inlineData.Get("data").String())
		if mimeType != "" && data != "" {
			addModerationImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
		}
	}
	if inlineData := part.Get("inlineData"); inlineData.IsObject() {
		mimeType := strings.TrimSpace(inlineData.Get("mimeType").String())
		data := strings.TrimSpace(inlineData.Get("data").String())
		if mimeType != "" && data != "" {
			addModerationImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
		}
	}
	addModerationImage(images, part.Get("file_data.file_uri").String())
	addModerationImage(images, part.Get("fileData.fileUri").String())
}

func addModerationImageData(images *[]string, mimeType string, data string) {
	mimeType = strings.TrimSpace(mimeType)
	data = strings.TrimSpace(data)
	if mimeType == "" || data == "" {
		return
	}
	addModerationImage(images, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
}

func addModerationImage(images *[]string, image string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return
	}
	if strings.HasPrefix(image, "data:") || strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		*images = append(*images, image)
	}
}

func normalizeModerationImages(images []string) []string {
	out := make([]string, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		out = append(out, image)
	}
	return out
}

func limitContentModerationImages(images []string) []string {
	if len(images) <= maxContentModerationInputImages {
		return images
	}
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(images))))
	if err != nil {
		return images[:maxContentModerationInputImages]
	}
	return []string{images[int(idx.Int64())]}
}

func addModerationText(parts *[]string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	*parts = append(*parts, text)
}

func normalizeContentModerationText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}
