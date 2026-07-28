package securityaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

const groqLimiterBucketIdleTTL = time.Hour

type groqTokenBucket struct {
	tokens   float64
	limit    int
	last     time.Time
	lastUsed time.Time
}

type groqTPMLimiter struct {
	mu      sync.Mutex
	buckets map[string]*groqTokenBucket
	now     func() time.Time
	wait    func(context.Context, time.Duration) error
}

func newGroqTPMLimiter() *groqTPMLimiter {
	return &groqTPMLimiter{
		buckets: make(map[string]*groqTokenBucket),
		now:     time.Now,
		wait:    waitForGroqTokens,
	}
}

func (l *groqTPMLimiter) acquire(ctx context.Context, key string, limit, cost int) error {
	if cost <= 0 {
		return nil
	}
	if limit <= 0 {
		limit = DefaultGroqTPMLimit
	}
	if cost > limit {
		return &GuardError{Code: ErrorCodeTPMBudgetExceeded, Retryable: false}
	}
	if l == nil {
		return &GuardError{Code: ErrorCodeUnavailable, Retryable: true}
	}
	for {
		if err := ctx.Err(); err != nil {
			return &GuardError{
				Code:      ErrorCodeUnavailable,
				Retryable: true,
				Timeout:   errors.Is(err, context.DeadlineExceeded),
				Cause:     err,
			}
		}
		now := l.currentTime()
		l.mu.Lock()
		if l.buckets == nil {
			l.buckets = make(map[string]*groqTokenBucket)
		}
		bucket := l.buckets[key]
		if bucket == nil {
			bucket = &groqTokenBucket{
				tokens:   float64(limit),
				limit:    limit,
				last:     now,
				lastUsed: now,
			}
			l.buckets[key] = bucket
		} else {
			refillGroqTokenBucket(bucket, now)
			if bucket.limit != limit {
				bucket.limit = limit
				if bucket.tokens > float64(limit) {
					bucket.tokens = float64(limit)
				}
			}
			bucket.lastUsed = now
		}
		if bucket.tokens >= float64(cost) {
			bucket.tokens -= float64(cost)
			l.cleanupIdleBucketsLocked(now, key)
			l.mu.Unlock()
			return nil
		}
		missing := float64(cost) - bucket.tokens
		waitDuration := time.Duration(math.Ceil(missing * float64(time.Minute) / float64(limit)))
		if waitDuration < time.Millisecond {
			waitDuration = time.Millisecond
		}
		l.cleanupIdleBucketsLocked(now, key)
		l.mu.Unlock()

		if err := l.waitForTokens(ctx, waitDuration); err != nil {
			return &GuardError{
				Code:      ErrorCodeUnavailable,
				Retryable: true,
				Timeout:   errors.Is(err, context.DeadlineExceeded),
				Cause:     err,
			}
		}
	}
}

func (l *groqTPMLimiter) currentTime() time.Time {
	if l != nil && l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *groqTPMLimiter) waitForTokens(ctx context.Context, duration time.Duration) error {
	if l != nil && l.wait != nil {
		return l.wait(ctx, duration)
	}
	return waitForGroqTokens(ctx, duration)
}

func (l *groqTPMLimiter) cleanupIdleBucketsLocked(now time.Time, activeKey string) {
	if len(l.buckets) < 128 {
		return
	}
	for key, bucket := range l.buckets {
		if key == activeKey || now.Sub(bucket.lastUsed) <= groqLimiterBucketIdleTTL {
			continue
		}
		delete(l.buckets, key)
	}
}

func refillGroqTokenBucket(bucket *groqTokenBucket, now time.Time) {
	if bucket == nil || bucket.limit <= 0 {
		return
	}
	elapsed := now.Sub(bucket.last)
	if elapsed <= 0 {
		return
	}
	bucket.tokens += elapsed.Seconds() * float64(bucket.limit) / time.Minute.Seconds()
	if bucket.tokens > float64(bucket.limit) {
		bucket.tokens = float64(bucket.limit)
	}
	bucket.last = now
}

func waitForGroqTokens(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func groqCredentialBucketKey(endpoint ActiveEndpoint) string {
	material := strings.TrimSpace(endpoint.Token)
	if material == "" {
		material = strings.TrimSpace(endpoint.BaseURL) + "\x00" + strings.TrimSpace(endpoint.ID)
	}
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
}

func estimateGroqRequestTokens(body []byte) (int, error) {
	inputTokens, err := countGroqTokens(string(body))
	if err != nil {
		return 0, err
	}
	return inputTokens + groqSafeguardMaxCompletionTokens, nil
}
