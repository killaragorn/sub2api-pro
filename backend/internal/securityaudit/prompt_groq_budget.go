package securityaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer"
)

const (
	groqDuplicateMinTokens   = 256
	groqMinimumMessageTokens = 96
	groqRecentMessageWindow  = 8
)

var (
	groqTokenizerOnce  sync.Once
	groqTokenizerCodec tokenizer.Codec
	groqTokenizerErr   error
	groqTokenizerMu    sync.Mutex
)

type groqBudgetMessage struct {
	role            string
	content         string
	originalTokens  int
	effectiveTokens int
	duplicateOf     int
	duplicate       bool
}

func buildGroqPromptScanChunk(messages []PromptAuditMessage, tokenBudget int) (PromptScanChunk, error) {
	if tokenBudget <= 0 {
		return PromptScanChunk{}, errors.New("prompt audit token budget must be positive")
	}
	prepared, originalTokens, duplicateCount, err := prepareGroqBudgetMessages(messages)
	if err != nil {
		return PromptScanChunk{}, err
	}
	if len(prepared) == 0 {
		return PromptScanChunk{}, nil
	}

	order, latestUser := groqMessagePriorityOrder(prepared)
	allocations := make([]int, len(prepared))
	selected := make([]bool, len(prepared))
	remaining := tokenBudget
	for _, index := range order {
		if selected[index] {
			continue
		}
		minimum := prepared[index].effectiveTokens
		if minimum > groqMinimumMessageTokens {
			minimum = groqMinimumMessageTokens
		}
		if minimum <= 0 || minimum > remaining {
			continue
		}
		selected[index] = true
		allocations[index] = minimum
		remaining -= minimum
	}
	distributeGroqTokenBudget(prepared, selected, allocations, latestUser, remaining)

	retained := make([]PromptAuditMessage, 0, len(prepared))
	retainedTokens := 0
	truncatedCount := 0
	omittedCount := 0
	for index, message := range prepared {
		if !selected[index] {
			omittedCount++
			continue
		}
		content := message.content
		if message.effectiveTokens > allocations[index] {
			content, err = truncateGroqMessage(content, allocations[index], message.effectiveTokens)
			if err != nil {
				return PromptScanChunk{}, err
			}
			truncatedCount++
		}
		count, countErr := countGroqTokens(content)
		if countErr != nil {
			return PromptScanChunk{}, countErr
		}
		if count == 0 || strings.TrimSpace(content) == "" {
			omittedCount++
			continue
		}
		retainedTokens += count
		retained = append(retained, PromptAuditMessage{Role: message.role, Content: content})
	}
	if retainedTokens > tokenBudget {
		return PromptScanChunk{}, errors.New("prompt audit token budget invariant violated")
	}
	return PromptScanChunk{
		Text:                     flattenPromptAuditMessages(retained),
		Messages:                 retained,
		OriginalTokenCount:       originalTokens,
		RetainedTokenCount:       retainedTokens,
		TruncatedMessageCount:    truncatedCount,
		DeduplicatedMessageCount: duplicateCount,
		OmittedMessageCount:      omittedCount,
	}, nil
}

func prepareGroqBudgetMessages(messages []PromptAuditMessage) ([]groqBudgetMessage, int, int, error) {
	prepared := make([]groqBudgetMessage, 0, len(messages))
	seen := make(map[[32]byte][]int)
	originalTokens := 0
	duplicateCount := 0
	for _, source := range messages {
		role := strings.ToLower(strings.TrimSpace(source.Role))
		content := strings.TrimSpace(source.Content)
		if !isPromptAuditRole(role) || content == "" {
			continue
		}
		tokenCount, err := countGroqTokens(content)
		if err != nil {
			return nil, 0, 0, err
		}
		originalTokens += tokenCount
		entry := groqBudgetMessage{
			role:            role,
			content:         content,
			originalTokens:  tokenCount,
			effectiveTokens: tokenCount,
			duplicateOf:     -1,
		}
		if tokenCount >= groqDuplicateMinTokens {
			digest := sha256.Sum256([]byte(role + "\x00" + content))
			for _, priorIndex := range seen[digest] {
				prior := prepared[priorIndex]
				if prior.role != role || prior.content != content {
					continue
				}
				entry.duplicate = true
				entry.duplicateOf = priorIndex
				entry.content = duplicateGroqMessageMarker(priorIndex, content)
				entry.effectiveTokens, err = countGroqTokens(entry.content)
				if err != nil {
					return nil, 0, 0, err
				}
				duplicateCount++
				break
			}
			if !entry.duplicate {
				seen[digest] = append(seen[digest], len(prepared))
			}
		}
		prepared = append(prepared, entry)
	}
	return prepared, originalTokens, duplicateCount, nil
}

func duplicateGroqMessageMarker(sourceIndex int, content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf(
		"[Sub2API audit: exact same-role duplicate omitted; source_message=%d; original_chars=%d; sha256=%s]",
		sourceIndex+1,
		utf8.RuneCountInString(content),
		hex.EncodeToString(digest[:]),
	)
}

func groqMessagePriorityOrder(messages []groqBudgetMessage) ([]int, int) {
	order := make([]int, 0, len(messages))
	seen := make(map[int]struct{}, len(messages))
	resolve := func(index int) int {
		for messages[index].duplicate && messages[index].duplicateOf >= 0 {
			index = messages[index].duplicateOf
		}
		return index
	}
	add := func(index int) {
		if index < 0 || index >= len(messages) {
			return
		}
		index = resolve(index)
		if _, exists := seen[index]; exists {
			return
		}
		seen[index] = struct{}{}
		order = append(order, index)
	}

	latestUser := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].role == "user" {
			latestUser = resolve(index)
			break
		}
	}
	add(latestUser)
	add(0)
	recentStart := len(messages) - groqRecentMessageWindow
	if recentStart < 0 {
		recentStart = 0
	}
	for index := len(messages) - 1; index >= recentStart; index-- {
		add(index)
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].role == "user" {
			add(index)
		}
	}
	for index := len(messages) - 1; index >= 0; index-- {
		add(index)
	}
	for index := range messages {
		add(index)
	}

	// Duplicate markers are cheap and preserve the original transcript
	// position, but their source content must always outrank the marker itself.
	for index := range messages {
		if !messages[index].duplicate {
			continue
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		order = append(order, index)
	}
	return order, latestUser
}

func distributeGroqTokenBudget(
	messages []groqBudgetMessage,
	selected []bool,
	allocations []int,
	latestUser int,
	remaining int,
) {
	for remaining > 0 {
		active := make([]int, 0, len(messages))
		totalWeight := 0
		for index := range messages {
			if !selected[index] || allocations[index] >= messages[index].effectiveTokens {
				continue
			}
			active = append(active, index)
			totalWeight += groqMessageWeight(messages, index, latestUser)
		}
		if len(active) == 0 || totalWeight == 0 {
			return
		}
		startingRemaining := remaining
		distributed := 0
		for _, index := range active {
			share := startingRemaining * groqMessageWeight(messages, index, latestUser) / totalWeight
			if share < 1 {
				share = 1
			}
			needed := messages[index].effectiveTokens - allocations[index]
			if share > needed {
				share = needed
			}
			if share > remaining {
				share = remaining
			}
			allocations[index] += share
			remaining -= share
			distributed += share
			if remaining == 0 {
				break
			}
		}
		if distributed == 0 {
			return
		}
	}
}

func groqMessageWeight(messages []groqBudgetMessage, index, latestUser int) int {
	weight := 1
	if index == latestUser {
		weight += 10
	}
	if index == 0 {
		weight += 4
	}
	if index >= len(messages)-groqRecentMessageWindow {
		weight += 3
	}
	if messages[index].role == "user" {
		weight += 2
	}
	return weight
}

func truncateGroqMessage(content string, tokenBudget, originalTokens int) (string, error) {
	if tokenBudget <= 0 {
		return "", nil
	}
	tokenIDs, pieces, err := encodeGroqTokens(content)
	if err != nil {
		return "", err
	}
	if len(tokenIDs) <= tokenBudget {
		return content, nil
	}
	digest := sha256.Sum256([]byte(content))
	originalChars := utf8.RuneCountInString(content)
	initialMarker := groqTruncationMarker(
		originalChars,
		originalChars,
		originalTokens,
		digest,
	)
	markerTokens, err := countGroqTokens(initialMarker + "\n\n")
	if err != nil {
		return "", err
	}
	availableTokens := tokenBudget - markerTokens
	if availableTokens < 0 {
		availableTokens = 0
	}
	headCount := availableTokens / 2
	tailCount := availableTokens - headCount
	if headCount+tailCount >= len(pieces) {
		tailCount = len(pieces) - headCount - 1
	}
	if tailCount < 0 {
		tailCount = 0
	}
	for headCount >= 0 && tailCount >= 0 {
		head := validGroqTokenPrefix(pieces, headCount)
		tail := validGroqTokenSuffix(pieces, tailCount)
		retainedChars := utf8.RuneCountInString(head) + utf8.RuneCountInString(tail)
		omittedChars := originalChars - retainedChars
		if omittedChars < 0 {
			omittedChars = 0
		}
		marker := groqTruncationMarker(
			omittedChars,
			originalChars,
			originalTokens,
			digest,
		)
		candidate := joinGroqTruncatedParts(head, marker, tail)
		count, countErr := countGroqTokens(candidate)
		if countErr != nil {
			return "", countErr
		}
		if count <= tokenBudget {
			return candidate, nil
		}
		if headCount == 0 && tailCount == 0 {
			return "", errors.New("prompt audit token budget cannot fit truncation marker")
		}
		excess := count - tokenBudget
		if excess < 1 {
			excess = 1
		}
		for excess > 0 && (headCount > 0 || tailCount > 0) {
			if tailCount >= headCount && tailCount > 0 {
				reduction := excess
				if reduction > tailCount {
					reduction = tailCount
				}
				tailCount -= reduction
				excess -= reduction
				continue
			}
			reduction := excess
			if reduction > headCount {
				reduction = headCount
			}
			headCount -= reduction
			excess -= reduction
		}
	}
	return "", errors.New("prompt audit message truncation failed")
}

func groqTruncationMarker(
	omittedChars int,
	originalChars int,
	originalTokens int,
	digest [sha256.Size]byte,
) string {
	return fmt.Sprintf(
		"[Sub2API audit: message middle omitted; omitted_chars=%d; original_chars=%d; original_tokens=%d; sha256=%s]",
		omittedChars,
		originalChars,
		originalTokens,
		hex.EncodeToString(digest[:]),
	)
}

func joinGroqTruncatedParts(head, marker, tail string) string {
	parts := make([]string, 0, 3)
	if head != "" {
		parts = append(parts, head)
	}
	parts = append(parts, marker)
	if tail != "" {
		parts = append(parts, tail)
	}
	return strings.Join(parts, "\n")
}

func validGroqTokenPrefix(pieces []string, count int) string {
	if count <= 0 {
		return ""
	}
	if count > len(pieces) {
		count = len(pieces)
	}
	value := strings.Join(pieces[:count], "")
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func validGroqTokenSuffix(pieces []string, count int) string {
	if count <= 0 {
		return ""
	}
	if count > len(pieces) {
		count = len(pieces)
	}
	value := strings.Join(pieces[len(pieces)-count:], "")
	for value != "" && !utf8.ValidString(value) {
		value = value[1:]
	}
	return value
}

func countGroqTokens(value string) (int, error) {
	codec, err := groqTokenizer()
	if err != nil {
		return 0, err
	}
	groqTokenizerMu.Lock()
	defer groqTokenizerMu.Unlock()
	return codec.Count(value)
}

func encodeGroqTokens(value string) ([]uint, []string, error) {
	codec, err := groqTokenizer()
	if err != nil {
		return nil, nil, err
	}
	groqTokenizerMu.Lock()
	defer groqTokenizerMu.Unlock()
	return codec.Encode(value)
}

func groqTokenizer() (tokenizer.Codec, error) {
	groqTokenizerOnce.Do(func() {
		groqTokenizerCodec, groqTokenizerErr = tokenizer.Get(tokenizer.O200kBase)
	})
	if groqTokenizerErr != nil {
		return nil, groqTokenizerErr
	}
	if groqTokenizerCodec == nil {
		return nil, errors.New("prompt audit tokenizer unavailable")
	}
	return groqTokenizerCodec, nil
}
