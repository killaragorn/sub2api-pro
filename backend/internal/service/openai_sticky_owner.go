package service

import (
	"context"
	"time"
)

// OpenAISessionOwnerCache is an optional atomic extension of GatewayCache.
// Production Redis caches implement it; the fallback keeps lightweight test
// caches and alternative implementations source-compatible.
type OpenAISessionOwnerCache interface {
	ClaimSessionAccount(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) (ownerID int64, claimed bool, err error)
	CompareAndSwapSessionAccount(ctx context.Context, groupID int64, sessionHash string, oldAccountID, newAccountID int64, ttl time.Duration) (bool, error)
	RefreshSessionTTLIfOwner(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) (bool, error)
	DeleteSessionAccountIfOwner(ctx context.Context, groupID int64, sessionHash string, accountID int64) (bool, error)
}

func claimSessionOwner(ctx context.Context, cache GatewayCache, groupID int64, sessionHash string, accountID int64, ttl time.Duration) (int64, bool, error) {
	if atomicCache, ok := cache.(OpenAISessionOwnerCache); ok {
		return atomicCache.ClaimSessionAccount(ctx, groupID, sessionHash, accountID, ttl)
	}
	ownerID, err := cache.GetSessionAccountID(ctx, groupID, sessionHash)
	if err == nil && ownerID > 0 {
		if ownerID == accountID {
			if refreshErr := cache.RefreshSessionTTL(ctx, groupID, sessionHash, ttl); refreshErr != nil {
				return 0, false, refreshErr
			}
		}
		return ownerID, false, nil
	}
	// Legacy GatewayCache implementations do not share a missing-key sentinel.
	// Production uses the atomic extension above; preserve the old best-effort
	// get-then-set behavior for lightweight tests and alternative caches.
	if err := cache.SetSessionAccountID(ctx, groupID, sessionHash, accountID, ttl); err != nil {
		return 0, false, err
	}
	return accountID, true, nil
}

func compareAndSwapSessionOwner(ctx context.Context, cache GatewayCache, groupID int64, sessionHash string, oldAccountID, newAccountID int64, ttl time.Duration) (bool, error) {
	if atomicCache, ok := cache.(OpenAISessionOwnerCache); ok {
		return atomicCache.CompareAndSwapSessionAccount(ctx, groupID, sessionHash, oldAccountID, newAccountID, ttl)
	}
	ownerID, err := cache.GetSessionAccountID(ctx, groupID, sessionHash)
	if err != nil {
		return false, err
	}
	if ownerID != oldAccountID {
		return false, nil
	}
	return true, cache.SetSessionAccountID(ctx, groupID, sessionHash, newAccountID, ttl)
}

func refreshSessionOwnerTTL(ctx context.Context, cache GatewayCache, groupID int64, sessionHash string, accountID int64, ttl time.Duration) (bool, error) {
	if atomicCache, ok := cache.(OpenAISessionOwnerCache); ok {
		return atomicCache.RefreshSessionTTLIfOwner(ctx, groupID, sessionHash, accountID, ttl)
	}
	ownerID, err := cache.GetSessionAccountID(ctx, groupID, sessionHash)
	if err != nil || ownerID != accountID {
		return false, err
	}
	return true, cache.RefreshSessionTTL(ctx, groupID, sessionHash, ttl)
}

func deleteSessionOwner(ctx context.Context, cache GatewayCache, groupID int64, sessionHash string, accountID int64) (bool, error) {
	if atomicCache, ok := cache.(OpenAISessionOwnerCache); ok {
		return atomicCache.DeleteSessionAccountIfOwner(ctx, groupID, sessionHash, accountID)
	}
	ownerID, err := cache.GetSessionAccountID(ctx, groupID, sessionHash)
	if err != nil || ownerID != accountID {
		return false, err
	}
	return true, cache.DeleteSessionAccountID(ctx, groupID, sessionHash)
}

func (s *OpenAIGatewayService) ClaimStickySession(ctx context.Context, groupID *int64, sessionHash string, accountID int64) (int64, bool, error) {
	if s == nil || s.cache == nil || sessionHash == "" || accountID <= 0 {
		return accountID, false, nil
	}
	ttl := s.openAIWSSessionStickyTTL()
	return s.claimStickySessionAccountID(ctx, groupID, sessionHash, accountID, ttl)
}

func (s *OpenAIGatewayService) MigrateStickySession(ctx context.Context, groupID *int64, sessionHash string, oldAccountID, newAccountID int64) (bool, error) {
	if s == nil || s.cache == nil || sessionHash == "" || oldAccountID <= 0 || newAccountID <= 0 {
		return false, nil
	}
	key := s.openAISessionCacheKey(sessionHash)
	if key == "" {
		return false, nil
	}
	return compareAndSwapSessionOwner(ctx, s.cache, derefGroupID(groupID), key, oldAccountID, newAccountID, s.openAIWSSessionStickyTTL())
}

// DeleteStickySessionOwner removes a binding only when accountID is still its
// canonical owner. It is used when post-selection validation rejects an
// account after the scheduler may already have claimed a new session.
func (s *OpenAIGatewayService) DeleteStickySessionOwner(ctx context.Context, groupID *int64, sessionHash string, accountID int64) (bool, error) {
	if s == nil || s.cache == nil || sessionHash == "" || accountID <= 0 {
		return false, nil
	}
	key := s.openAISessionCacheKey(sessionHash)
	if key == "" {
		return false, nil
	}
	return deleteSessionOwner(ctx, s.cache, derefGroupID(groupID), key, accountID)
}
