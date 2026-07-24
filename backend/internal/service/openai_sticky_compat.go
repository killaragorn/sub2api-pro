package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
)

type openAILegacySessionHashContextKey struct{}

var openAILegacySessionHashKey = openAILegacySessionHashContextKey{}

var (
	openAIStickyLegacyReadFallbackTotal atomic.Int64
	openAIStickyLegacyReadFallbackHit   atomic.Int64
	openAIStickyLegacyDualWriteTotal    atomic.Int64
	openAIStickyLegacyDualWriteError    atomic.Int64
)

func openAIStickyCompatStats() (legacyReadFallbackTotal, legacyReadFallbackHit, legacyDualWriteTotal int64) {
	return openAIStickyLegacyReadFallbackTotal.Load(),
		openAIStickyLegacyReadFallbackHit.Load(),
		openAIStickyLegacyDualWriteTotal.Load()
}

func openAIStickyLegacyDualWriteErrorCount() int64 {
	return openAIStickyLegacyDualWriteError.Load()
}

// DeriveSessionHashFromSeed computes the current-format sticky-session hash
// from an arbitrary seed string.
func DeriveSessionHashFromSeed(seed string) string {
	currentHash, _ := deriveOpenAISessionHashes(seed)
	return currentHash
}

func deriveOpenAISessionHashes(sessionID string) (currentHash string, legacyHash string) {
	normalized := strings.TrimSpace(sessionID)
	if normalized == "" {
		return "", ""
	}

	currentHash = fmt.Sprintf("%016x", xxhash.Sum64String(normalized))
	sum := sha256.Sum256([]byte(normalized))
	legacyHash = hex.EncodeToString(sum[:])
	return currentHash, legacyHash
}

func withOpenAILegacySessionHash(ctx context.Context, legacyHash string) context.Context {
	if ctx == nil {
		return nil
	}
	trimmed := strings.TrimSpace(legacyHash)
	if trimmed == "" {
		return ctx
	}
	return context.WithValue(ctx, openAILegacySessionHashKey, trimmed)
}

func openAILegacySessionHashFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(openAILegacySessionHashKey).(string)
	return strings.TrimSpace(value)
}

func attachOpenAILegacySessionHashToGin(c *gin.Context, legacyHash string) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request = c.Request.WithContext(withOpenAILegacySessionHash(c.Request.Context(), legacyHash))
}

func (s *OpenAIGatewayService) openAISessionHashReadOldFallbackEnabled() bool {
	if s == nil || s.cfg == nil {
		return true
	}
	return s.cfg.Gateway.OpenAIWS.SessionHashReadOldFallback
}

func (s *OpenAIGatewayService) openAISessionHashDualWriteOldEnabled() bool {
	if s == nil || s.cfg == nil {
		return true
	}
	return s.cfg.Gateway.OpenAIWS.SessionHashDualWriteOld
}

func (s *OpenAIGatewayService) openAISessionCacheKey(sessionHash string) string {
	normalized := strings.TrimSpace(sessionHash)
	if normalized == "" {
		return ""
	}
	return "openai:" + normalized
}

func (s *OpenAIGatewayService) openAILegacySessionCacheKey(ctx context.Context, sessionHash string) string {
	legacyHash := openAILegacySessionHashFromContext(ctx)
	if legacyHash == "" {
		return ""
	}
	legacyKey := "openai:" + legacyHash
	if legacyKey == s.openAISessionCacheKey(sessionHash) {
		return ""
	}
	return legacyKey
}

func (s *OpenAIGatewayService) openAIStickyLegacyTTL(ttl time.Duration) time.Duration {
	legacyTTL := ttl
	if legacyTTL <= 0 {
		legacyTTL = openaiStickySessionTTL
	}
	if legacyTTL > 10*time.Minute {
		return 10 * time.Minute
	}
	return legacyTTL
}

func (s *OpenAIGatewayService) getStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string) (int64, error) {
	if s == nil || s.cache == nil {
		return 0, nil
	}

	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return 0, nil
	}

	accountID, err := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), primaryKey)
	if err == nil && accountID > 0 {
		return accountID, nil
	}
	if !s.openAISessionHashReadOldFallbackEnabled() {
		return accountID, err
	}

	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey == "" {
		return accountID, err
	}

	openAIStickyLegacyReadFallbackTotal.Add(1)
	legacyAccountID, legacyErr := s.cache.GetSessionAccountID(ctx, derefGroupID(groupID), legacyKey)
	if legacyErr == nil && legacyAccountID > 0 {
		openAIStickyLegacyReadFallbackHit.Add(1)
		ownerID, _, claimErr := claimSessionOwner(
			ctx,
			s.cache,
			derefGroupID(groupID),
			primaryKey,
			legacyAccountID,
			s.openAIWSSessionStickyTTL(),
		)
		if claimErr != nil {
			return 0, claimErr
		}
		return ownerID, nil
	}
	return accountID, err
}

func (s *OpenAIGatewayService) setStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string, accountID int64, ttl time.Duration) error {
	_, _, err := s.claimStickySessionAccountID(ctx, groupID, sessionHash, accountID, ttl)
	return err
}

func (s *OpenAIGatewayService) claimStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string, accountID int64, ttl time.Duration) (int64, bool, error) {
	if s == nil || s.cache == nil || accountID <= 0 {
		return accountID, false, nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return accountID, false, nil
	}

	ownerID, claimed, err := claimSessionOwner(ctx, s.cache, derefGroupID(groupID), primaryKey, accountID, ttl)
	if err != nil {
		return 0, false, err
	}

	if !s.openAISessionHashDualWriteOldEnabled() {
		return ownerID, claimed, nil
	}
	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey == "" {
		return ownerID, claimed, nil
	}
	if _, _, err := claimSessionOwner(ctx, s.cache, derefGroupID(groupID), legacyKey, ownerID, s.openAIStickyLegacyTTL(ttl)); err != nil {
		// The current-format key is authoritative. A compatibility-key failure
		// must not turn a successful canonical claim into an unavailable request.
		openAIStickyLegacyDualWriteError.Add(1)
		return ownerID, claimed, nil
	}
	openAIStickyLegacyDualWriteTotal.Add(1)
	return ownerID, claimed, nil
}

func (s *OpenAIGatewayService) claimStickySessionAccountIDWithRollbackToken(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	accountID int64,
	ttl time.Duration,
	rollbackToken string,
) (ownerID int64, claimed bool, guarded bool, legacyClaimed bool, err error) {
	if s == nil || s.cache == nil || accountID <= 0 {
		return accountID, false, false, false, nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return accountID, false, false, false, nil
	}

	ownerID, claimed, guarded, err = claimSessionOwnerWithRollbackToken(
		ctx,
		s.cache,
		derefGroupID(groupID),
		primaryKey,
		accountID,
		ttl,
		rollbackToken,
	)
	if err != nil {
		return 0, false, false, false, err
	}
	if !s.openAISessionHashDualWriteOldEnabled() {
		return ownerID, claimed, guarded, false, nil
	}

	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey == "" {
		return ownerID, claimed, guarded, false, nil
	}
	legacyOwnerID, legacyClaimed, legacyErr := claimSessionOwner(
		ctx,
		s.cache,
		derefGroupID(groupID),
		legacyKey,
		ownerID,
		s.openAIStickyLegacyTTL(ttl),
	)
	if legacyErr != nil {
		openAIStickyLegacyDualWriteError.Add(1)
		return ownerID, claimed, guarded, false, nil
	}
	openAIStickyLegacyDualWriteTotal.Add(1)
	return ownerID, claimed, guarded, legacyClaimed && legacyOwnerID == ownerID, nil
}

func (s *OpenAIGatewayService) refreshStickySessionTTL(ctx context.Context, groupID *int64, sessionHash string, accountID int64, ttl time.Duration) error {
	if s == nil || s.cache == nil || accountID <= 0 {
		return nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	_, err := refreshSessionOwnerTTL(ctx, s.cache, derefGroupID(groupID), primaryKey, accountID, ttl)
	if !s.openAISessionHashReadOldFallbackEnabled() && !s.openAISessionHashDualWriteOldEnabled() {
		return err
	}

	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey != "" {
		_, _ = refreshSessionOwnerTTL(ctx, s.cache, derefGroupID(groupID), legacyKey, accountID, s.openAIStickyLegacyTTL(ttl))
	}
	return err
}

func (s *OpenAIGatewayService) deleteStickySessionAccountID(ctx context.Context, groupID *int64, sessionHash string, accountID int64) error {
	if s == nil || s.cache == nil || accountID <= 0 {
		return nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return nil
	}

	_, err := deleteSessionOwner(ctx, s.cache, derefGroupID(groupID), primaryKey, accountID)
	if !s.openAISessionHashReadOldFallbackEnabled() && !s.openAISessionHashDualWriteOldEnabled() {
		return err
	}

	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey != "" {
		_, _ = deleteSessionOwner(ctx, s.cache, derefGroupID(groupID), legacyKey, accountID)
	}
	return err
}
