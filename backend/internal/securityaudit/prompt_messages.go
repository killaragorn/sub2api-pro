package securityaudit

import (
	"encoding/json"
	"errors"
	"strings"
)

const promptAuditPayloadPrefix = "sub2api-prompt-audit-v1:"

type promptAuditTransientPayload struct {
	ScanText string               `json:"scan_text"`
	Messages []PromptAuditMessage `json:"messages,omitempty"`
}

func encodePromptAuditPayload(snapshot PromptSnapshot) (string, error) {
	payload, err := json.Marshal(promptAuditTransientPayload{
		ScanText: snapshot.ScanText,
		Messages: snapshot.AuditMessages,
	})
	if err != nil {
		return "", err
	}
	return promptAuditPayloadPrefix + string(payload), nil
}

func decodePromptAuditPayload(value string) (promptAuditTransientPayload, error) {
	if !strings.HasPrefix(value, promptAuditPayloadPrefix) {
		return promptAuditTransientPayload{ScanText: value}, nil
	}
	var payload promptAuditTransientPayload
	if err := json.Unmarshal([]byte(strings.TrimPrefix(value, promptAuditPayloadPrefix)), &payload); err != nil {
		return promptAuditTransientPayload{}, errors.New("prompt audit payload is invalid")
	}
	if payload.ScanText == "" {
		return promptAuditTransientPayload{}, errors.New("prompt audit payload contains no scan text")
	}
	for _, message := range payload.Messages {
		if !isPromptAuditRole(message.Role) || strings.TrimSpace(message.Content) == "" {
			return promptAuditTransientPayload{}, errors.New("prompt audit payload contains an invalid message")
		}
	}
	return payload, nil
}

func buildPromptScanChunks(snapshot PromptSnapshot, endpoints []ActiveEndpoint, inputLimit int) ([]PromptScanChunk, error) {
	if promptAuditPoolUsesStructuredMessages(endpoints) {
		messages := snapshot.AuditMessages
		if len(messages) == 0 && strings.TrimSpace(snapshot.ScanText) != "" {
			legacyText := strings.ReplaceAll(snapshot.ScanText, promptAuditPrioritySeparator, "\n\n")
			messages = []PromptAuditMessage{{Role: "user", Content: legacyText}}
		}
		chunk, err := buildGroqPromptScanChunk(messages, inputLimit)
		if err != nil {
			return nil, err
		}
		if len(chunk.Messages) == 0 {
			return nil, nil
		}
		return []PromptScanChunk{chunk}, nil
	}
	textChunks := SplitRunes(snapshot.ScanText, inputLimit)
	chunks := make([]PromptScanChunk, 0, len(textChunks))
	for _, textChunk := range textChunks {
		chunks = append(chunks, PromptScanChunk{Text: textChunk})
	}
	return chunks, nil
}

func splitPromptAuditMessages(messages []PromptAuditMessage, limit int) []PromptScanChunk {
	if limit <= 0 {
		return nil
	}
	chunks := make([]PromptScanChunk, 0, len(messages))
	current := make([]PromptAuditMessage, 0, len(messages))
	currentRunes := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		cloned := append([]PromptAuditMessage(nil), current...)
		chunks = append(chunks, PromptScanChunk{
			Text:     flattenPromptAuditMessages(cloned),
			Messages: cloned,
		})
		current = current[:0]
		currentRunes = 0
	}

	for _, message := range messages {
		contentRunes := []rune(message.Content)
		if len(contentRunes) == 0 || !isPromptAuditRole(message.Role) {
			continue
		}
		if len(contentRunes) <= limit {
			separatorRunes := 0
			if currentRunes > 0 {
				separatorRunes = 2
			}
			if currentRunes > 0 && currentRunes+separatorRunes+len(contentRunes) > limit {
				flush()
				separatorRunes = 0
			}
			current = append(current, message)
			currentRunes += separatorRunes + len(contentRunes)
			continue
		}

		flush()
		for start := 0; start < len(contentRunes); start += limit {
			end := start + limit
			if end > len(contentRunes) {
				end = len(contentRunes)
			}
			part := PromptAuditMessage{Role: message.Role, Content: string(contentRunes[start:end])}
			chunks = append(chunks, PromptScanChunk{Text: part.Content, Messages: []PromptAuditMessage{part}})
		}
	}
	flush()
	return chunks
}

func flattenPromptAuditMessages(messages []PromptAuditMessage) string {
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.Content) != "" {
			contents = append(contents, message.Content)
		}
	}
	return strings.Join(contents, "\n\n")
}

func promptAuditPoolUsesStructuredMessages(endpoints []ActiveEndpoint) bool {
	for _, endpoint := range endpoints {
		if endpoint.Enabled && (strings.EqualFold(strings.TrimSpace(endpoint.Protocol), EndpointProtocolGroqSafeguard) ||
			strings.EqualFold(strings.TrimSpace(endpoint.Model), DefaultGroqSafeguardModel)) {
			return true
		}
	}
	return false
}

func isPromptAuditRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system", "developer", "assistant", "user":
		return true
	default:
		return false
	}
}
