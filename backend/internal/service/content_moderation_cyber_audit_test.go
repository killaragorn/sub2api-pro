package service

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestBuildCyberPolicyRequestSnapshot_RedactsSecretsAndInlineBinary(t *testing.T) {
	raw := []byte(`{
  "model":"gpt-5",
  "authorization":"Bearer should-not-survive",
  "input":"inspect api_key=sk-proj-1234567890abcdef and xai-1234567890abcdef and keep this request text",
  "image":{"type":"input_image","media_type":"image/png","data":"aGVsbG8="},
  "input_audio":{"data":"YXVkaW8=","format":"wav"},
  "image_url":"https://example.com/image.png?token=signed-secret&X-Amz-Signature=aws-signature&width=200"
}`)

	snapshot := buildCyberPolicyRequestSnapshot(raw)

	require.False(t, snapshot.Truncated)
	require.Equal(t, int64(len(raw)), snapshot.OriginalBytes)
	require.Equal(t, len(snapshot.Body), snapshot.StoredBytes)
	require.True(t, json.Valid([]byte(snapshot.Body)))
	require.NotContains(t, snapshot.Body, "should-not-survive")
	require.NotContains(t, snapshot.Body, "sk-proj-1234567890abcdef")
	require.NotContains(t, snapshot.Body, "xai-1234567890abcdef")
	require.NotContains(t, snapshot.Body, "signed-secret")
	require.NotContains(t, snapshot.Body, "aws-signature")
	require.NotContains(t, snapshot.Body, "aGVsbG8=")
	require.NotContains(t, snapshot.Body, "YXVkaW8=")
	require.Contains(t, snapshot.Body, "keep this request text")
	require.Contains(t, snapshot.Body, "inline binary omitted")
	require.Contains(t, snapshot.Body, "sha256=")
	require.Contains(t, snapshot.Body, "width=200")
}

func TestBuildCyberPolicyRequestSnapshot_RedactsCredentialInURLPath(t *testing.T) {
	raw := []byte(`{"input":"https://example.com/files/sk-proj-1234567890abcdef/result"}`)

	snapshot := buildCyberPolicyRequestSnapshot(raw)

	require.NotContains(t, snapshot.Body, "sk-proj-1234567890abcdef")
	require.Contains(t, snapshot.Body, "%5Bredacted%5D")
}

func TestBuildCyberPolicyRequestSnapshot_TruncatesWithUTF8HeadAndTail(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"a_head": strings.Repeat("头", 30000),
		"z_tail": strings.Repeat("尾", 30000),
	})
	require.NoError(t, err)

	snapshot := buildCyberPolicyRequestSnapshot(raw)

	require.True(t, snapshot.Truncated)
	require.LessOrEqual(t, snapshot.StoredBytes, cyberPolicyRequestSnapshotMaxBytes)
	require.True(t, utf8.ValidString(snapshot.Body))
	require.Contains(t, snapshot.Body, cyberPolicySnapshotTruncationMarker)
	require.Contains(t, snapshot.Body, "头")
	require.Contains(t, snapshot.Body, "尾")
}

func TestBuildCyberPolicyRequestSnapshot_PreservesJSONNumberPrecision(t *testing.T) {
	raw := []byte(`{"seed":9007199254740993,"temperature":1.2300,"input":"audit"}`)

	snapshot := buildCyberPolicyRequestSnapshot(raw)

	require.False(t, snapshot.Truncated)
	require.Contains(t, snapshot.Body, `"seed":9007199254740993`)
	require.Contains(t, snapshot.Body, `"temperature":1.2300`)
}

func TestBuildCyberPolicyRequestSnapshot_InvalidJSONUsesMetadataPlaceholder(t *testing.T) {
	raw := []byte(`{"input":`)
	snapshot := buildCyberPolicyRequestSnapshot(raw)

	require.Equal(t, int64(len(raw)), snapshot.OriginalBytes)
	require.Contains(t, snapshot.Body, "invalid JSON request omitted")
	require.NotContains(t, snapshot.Body, string(raw))
}
