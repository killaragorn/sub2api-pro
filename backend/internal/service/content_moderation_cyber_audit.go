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

func buildCyberPolicyRequestSnapshot(raw []byte) cyberPolicyRequestSnapshot {
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

	redacted := redactCyberPolicyAuditValue(value, "", nil, 0)
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
