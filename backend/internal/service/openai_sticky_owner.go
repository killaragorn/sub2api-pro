package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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

// OpenAISessionOwnerRollbackCache guards a provisional owner mutation with a
// one-shot token. A confirmed or non-provisional owner consumer invalidates
// the token, so a handler cannot roll back state already accepted by another
// request while two still-provisional selections can both reject safely.
type OpenAISessionOwnerRollbackCache interface {
	ClaimSessionAccountWithRollbackToken(
		ctx context.Context,
		groupID int64,
		sessionHash string,
		accountID int64,
		ttl time.Duration,
		rollbackToken string,
	) (ownerID int64, claimed bool, err error)
	CompareAndSwapSessionAccountWithRollbackToken(
		ctx context.Context,
		groupID int64,
		sessionHash string,
		oldAccountID int64,
		newAccountID int64,
		ttl time.Duration,
		rollbackToken string,
	) (bool, error)
	RollbackSessionAccount(
		ctx context.Context,
		groupID int64,
		sessionHash string,
		currentAccountID int64,
		previousAccountID int64,
		ttl time.Duration,
		rollbackToken string,
	) (bool, error)
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

func claimSessionOwnerWithRollbackToken(
	ctx context.Context,
	cache GatewayCache,
	groupID int64,
	sessionHash string,
	accountID int64,
	ttl time.Duration,
	rollbackToken string,
) (ownerID int64, claimed bool, guarded bool, err error) {
	if rollbackCache, ok := cache.(OpenAISessionOwnerRollbackCache); ok && rollbackToken != "" {
		ownerID, claimed, err = rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			accountID,
			ttl,
			rollbackToken,
		)
		return ownerID, claimed, claimed && err == nil, err
	}
	ownerID, claimed, err = claimSessionOwner(ctx, cache, groupID, sessionHash, accountID, ttl)
	return ownerID, claimed, false, err
}

func compareAndSwapSessionOwnerWithRollbackToken(
	ctx context.Context,
	cache GatewayCache,
	groupID int64,
	sessionHash string,
	oldAccountID int64,
	newAccountID int64,
	ttl time.Duration,
	rollbackToken string,
) (swapped bool, guarded bool, err error) {
	if rollbackCache, ok := cache.(OpenAISessionOwnerRollbackCache); ok && rollbackToken != "" {
		swapped, err = rollbackCache.CompareAndSwapSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			oldAccountID,
			newAccountID,
			ttl,
			rollbackToken,
		)
		return swapped, swapped && err == nil, err
	}
	swapped, err = compareAndSwapSessionOwner(
		ctx,
		cache,
		groupID,
		sessionHash,
		oldAccountID,
		newAccountID,
		ttl,
	)
	return swapped, false, err
}

func rollbackSessionOwner(
	ctx context.Context,
	cache GatewayCache,
	groupID int64,
	sessionHash string,
	currentAccountID int64,
	previousAccountID int64,
	ttl time.Duration,
	rollbackToken string,
) (bool, error) {
	rollbackCache, ok := cache.(OpenAISessionOwnerRollbackCache)
	if !ok || rollbackToken == "" {
		return false, nil
	}
	return rollbackCache.RollbackSessionAccount(
		ctx,
		groupID,
		sessionHash,
		currentAccountID,
		previousAccountID,
		ttl,
		rollbackToken,
	)
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

func (s *OpenAIGatewayService) claimStickySessionForSelection(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	accountID int64,
) (ownerID int64, claimed bool, rollbackToken string, legacyClaimed bool, err error) {
	if s == nil || s.cache == nil || sessionHash == "" || accountID <= 0 {
		return accountID, false, "", false, nil
	}
	if _, ok := s.cache.(OpenAISessionOwnerRollbackCache); !ok {
		ownerID, claimed, err = s.ClaimStickySession(ctx, groupID, sessionHash, accountID)
		return ownerID, claimed, "", false, err
	}

	candidateToken := uuid.NewString()
	ownerID, claimed, guarded, legacyClaimed, err := s.claimStickySessionAccountIDWithRollbackToken(
		ctx,
		groupID,
		sessionHash,
		accountID,
		s.openAIWSSessionStickyTTL(),
		candidateToken,
	)
	if guarded {
		rollbackToken = candidateToken
	}
	return ownerID, claimed, rollbackToken, legacyClaimed, err
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

func (s *OpenAIGatewayService) migrateStickySessionForSelection(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	oldAccountID int64,
	newAccountID int64,
) (swapped bool, rollbackToken string, err error) {
	if s == nil || s.cache == nil || sessionHash == "" || oldAccountID <= 0 || newAccountID <= 0 {
		return false, "", nil
	}
	key := s.openAISessionCacheKey(sessionHash)
	if key == "" {
		return false, "", nil
	}
	if _, ok := s.cache.(OpenAISessionOwnerRollbackCache); !ok {
		swapped, err = s.MigrateStickySession(ctx, groupID, sessionHash, oldAccountID, newAccountID)
		return swapped, "", err
	}

	candidateToken := uuid.NewString()
	swapped, guarded, err := compareAndSwapSessionOwnerWithRollbackToken(
		ctx,
		s.cache,
		derefGroupID(groupID),
		key,
		oldAccountID,
		newAccountID,
		s.openAIWSSessionStickyTTL(),
		candidateToken,
	)
	if guarded {
		rollbackToken = candidateToken
	}
	return swapped, rollbackToken, err
}

func (s *OpenAIGatewayService) rollbackStickySessionForSelection(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	currentAccountID int64,
	previousAccountID int64,
	rollbackToken string,
	legacyClaimed bool,
) (bool, error) {
	if s == nil || s.cache == nil || sessionHash == "" || currentAccountID <= 0 || rollbackToken == "" {
		return false, nil
	}
	primaryKey := s.openAISessionCacheKey(sessionHash)
	if primaryKey == "" {
		return false, nil
	}
	rolledBack, err := rollbackSessionOwner(
		ctx,
		s.cache,
		derefGroupID(groupID),
		primaryKey,
		currentAccountID,
		previousAccountID,
		s.openAIWSSessionStickyTTL(),
		rollbackToken,
	)
	if err != nil || !rolledBack {
		return rolledBack, err
	}

	if !legacyClaimed {
		return true, nil
	}
	legacyKey := s.openAILegacySessionCacheKey(ctx, sessionHash)
	if legacyKey != "" {
		_, _ = deleteSessionOwner(
			ctx,
			s.cache,
			derefGroupID(groupID),
			legacyKey,
			currentAccountID,
		)
	}
	return true, nil
}

// ConfirmAcquiredOpenAISelection commits a selection after handler-level
// validation. A provisional guarded mutation remains rollbackable until this
// point; confirming any canonical consumer invalidates that mutation's token.
func (s *OpenAIGatewayService) ConfirmAcquiredOpenAISelection(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	selection *AccountSelectionResult,
) error {
	if s == nil || selection == nil || !selection.Acquired || selection.Account == nil ||
		sessionHash == "" || selection.PreserveStickyBinding {
		return nil
	}
	accountID := selection.Account.ID
	ownerID, _, err := s.ClaimStickySession(ctx, groupID, sessionHash, accountID)
	if err != nil {
		return err
	}
	if ownerID != accountID {
		return fmt.Errorf(
			"sticky session owner changed before selection confirmation: selected=%d owner=%d",
			accountID,
			ownerID,
		)
	}
	selection.stickyBindingRollbackToken = ""
	selection.stickyBindingLegacyClaimed = false
	return nil
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
