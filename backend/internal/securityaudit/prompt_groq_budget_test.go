package securityaudit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestGroqPromptBudgetBuildsOneOrderedBoundedConversation(t *testing.T) {
	repeated := "developer-start " + strings.Repeat("shared fixed instructions ", 500) + " developer-end"
	messages := []PromptAuditMessage{
		{Role: "system", Content: "system-start " + strings.Repeat("system policy ", 500) + " system-end"},
		{Role: "developer", Content: repeated},
		{Role: "developer", Content: repeated},
		{Role: "assistant", Content: "recent assistant context"},
		{Role: "user", Content: "latest-user-start " + strings.Repeat("latest request ", 500) + " latest-user-end"},
	}
	snapshot := PromptSnapshot{
		ScanText:      flattenPromptAuditMessages(messages),
		AuditMessages: messages,
	}
	chunks, err := buildPromptScanChunks(
		snapshot,
		[]ActiveEndpoint{{Protocol: EndpointProtocolGroqSafeguard, Enabled: true}},
		800,
	)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	chunk := chunks[0]
	require.LessOrEqual(t, chunk.RetainedTokenCount, 800)
	require.Greater(t, chunk.OriginalTokenCount, chunk.RetainedTokenCount)
	require.GreaterOrEqual(t, chunk.TruncatedMessageCount, 2)
	require.Equal(t, 1, chunk.DeduplicatedMessageCount)
	require.Zero(t, chunk.OmittedMessageCount)
	require.Equal(t, []string{"system", "developer", "developer", "assistant", "user"}, promptAuditRoles(chunk.Messages))
	require.Contains(t, chunk.Messages[0].Content, "system-start")
	require.Contains(t, chunk.Messages[0].Content, "system-end")
	require.Contains(t, chunk.Messages[0].Content, "message middle omitted")
	require.Contains(t, chunk.Messages[0].Content, "sha256=")
	require.Contains(t, chunk.Messages[2].Content, "exact same-role duplicate omitted")
	require.Contains(t, chunk.Messages[4].Content, "latest-user-start")
	require.Contains(t, chunk.Messages[4].Content, "latest-user-end")
	for _, message := range chunk.Messages {
		require.True(t, utf8.ValidString(message.Content))
	}
}

func TestGroqPromptBudgetPrioritizesLatestUserThenConversationHead(t *testing.T) {
	messages := []PromptAuditMessage{
		{Role: "system", Content: "HEAD-CONTEXT " + strings.Repeat("head ", 500)},
		{Role: "assistant", Content: "MIDDLE-CONTEXT " + strings.Repeat("middle ", 500)},
		{Role: "user", Content: "LATEST-USER " + strings.Repeat("latest ", 500)},
	}
	chunk, err := buildGroqPromptScanChunk(messages, 192)
	require.NoError(t, err)
	require.LessOrEqual(t, chunk.RetainedTokenCount, 192)
	require.Equal(t, []string{"system", "user"}, promptAuditRoles(chunk.Messages))
	require.Contains(t, chunk.Messages[0].Content, "HEAD-CONTEXT")
	require.Contains(t, chunk.Messages[1].Content, "LATEST-USER")
	require.Equal(t, 1, chunk.OmittedMessageCount)
}

func TestPromptChunkingKeepsQwenCharacterChunksAndGroqSingleCall(t *testing.T) {
	snapshot := PromptSnapshot{
		ScanText: "abcdef",
		AuditMessages: []PromptAuditMessage{
			{Role: "system", Content: "abc"},
			{Role: "user", Content: "def"},
		},
	}
	qwen, err := buildPromptScanChunks(
		snapshot,
		[]ActiveEndpoint{{Protocol: EndpointProtocolQwen3Guard, Enabled: true}},
		3,
	)
	require.NoError(t, err)
	require.Len(t, qwen, 2)
	require.Equal(t, []string{"abc", "def"}, []string{qwen[0].Text, qwen[1].Text})

	groq, err := buildPromptScanChunks(
		snapshot,
		[]ActiveEndpoint{{Protocol: EndpointProtocolGroqSafeguard, Enabled: true}},
		128,
	)
	require.NoError(t, err)
	require.Len(t, groq, 1)
	require.Equal(t, []string{"system", "user"}, promptAuditRoles(groq[0].Messages))
}

func TestGroqTPMLimiterUsesCredentialBucketAndWaitsForRefill(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	var waits []time.Duration
	limiter := &groqTPMLimiter{
		buckets: make(map[string]*groqTokenBucket),
		now:     func() time.Time { return now },
		wait: func(_ context.Context, duration time.Duration) error {
			waits = append(waits, duration)
			now = now.Add(duration)
			return nil
		},
	}
	require.NoError(t, limiter.acquire(context.Background(), "credential", 1000, 600))
	require.NoError(t, limiter.acquire(context.Background(), "credential", 1000, 600))
	require.Equal(t, []time.Duration{12 * time.Second}, waits)

	err := limiter.acquire(context.Background(), "credential", 1000, 1001)
	var guardErr *GuardError
	require.ErrorAs(t, err, &guardErr)
	require.Equal(t, ErrorCodeTPMBudgetExceeded, guardErr.Code)
	require.False(t, guardErr.Retryable)
	require.Equal(t, "Prompt Guard request exceeds the configured TPM budget", stableErrorMessage(guardErr.Code))

	limiter.wait = waitForGroqTokens
	require.NoError(t, limiter.acquire(context.Background(), "empty", 1000, 1000))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = limiter.acquire(cancelled, "empty", 1000, 1)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestGroqCredentialBucketKeyDoesNotExposeOrSplitSameKey(t *testing.T) {
	first := groqCredentialBucketKey(ActiveEndpoint{ID: "one", BaseURL: "https://one.example", Token: "secret-key"})
	second := groqCredentialBucketKey(ActiveEndpoint{ID: "two", BaseURL: "https://two.example", Token: "secret-key"})
	require.Equal(t, first, second)
	require.NotContains(t, first, "secret-key")
	require.Len(t, first, 64)
}

func promptAuditRoles(messages []PromptAuditMessage) []string {
	roles := make([]string, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}
