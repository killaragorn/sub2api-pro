package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newSessionOwnerTestCache(t *testing.T) (service.OpenAISessionOwnerCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewGatewayCache(client).(service.OpenAISessionOwnerCache)
	require.True(t, ok)
	return cache, server
}

func TestGatewayCacheSessionOwnerLifecycle(t *testing.T) {
	cache, server := newSessionOwnerTestCache(t)
	ctx := context.Background()
	const (
		groupID     = int64(7)
		sessionHash = "owner-lifecycle"
	)

	ownerID, claimed, err := cache.ClaimSessionAccount(ctx, groupID, sessionHash, 101, time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(101), ownerID)
	require.True(t, claimed)

	server.FastForward(40 * time.Second)
	require.Less(t, server.TTL(buildSessionKey(groupID, sessionHash)), 30*time.Second)
	ownerID, claimed, err = cache.ClaimSessionAccount(ctx, groupID, sessionHash, 101, time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(101), ownerID)
	require.False(t, claimed)
	require.Equal(t, time.Minute, server.TTL(buildSessionKey(groupID, sessionHash)))

	ownerID, claimed, err = cache.ClaimSessionAccount(ctx, groupID, sessionHash, 202, time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(101), ownerID)
	require.False(t, claimed)

	swapped, err := cache.CompareAndSwapSessionAccount(ctx, groupID, sessionHash, 999, 202, time.Minute)
	require.NoError(t, err)
	require.False(t, swapped)
	swapped, err = cache.CompareAndSwapSessionAccount(ctx, groupID, sessionHash, 101, 202, time.Minute)
	require.NoError(t, err)
	require.True(t, swapped)

	refreshed, err := cache.RefreshSessionTTLIfOwner(ctx, groupID, sessionHash, 101, 2*time.Minute)
	require.NoError(t, err)
	require.False(t, refreshed)
	refreshed, err = cache.RefreshSessionTTLIfOwner(ctx, groupID, sessionHash, 202, 2*time.Minute)
	require.NoError(t, err)
	require.True(t, refreshed)

	deleted, err := cache.DeleteSessionAccountIfOwner(ctx, groupID, sessionHash, 101)
	require.NoError(t, err)
	require.False(t, deleted)
	deleted, err = cache.DeleteSessionAccountIfOwner(ctx, groupID, sessionHash, 202)
	require.NoError(t, err)
	require.True(t, deleted)
	require.False(t, server.Exists(buildSessionKey(groupID, sessionHash)))
}

func TestGatewayCacheConcurrentSessionClaimsConvergeToOneOwner(t *testing.T) {
	cache, _ := newSessionOwnerTestCache(t)
	ctx := context.Background()
	const contenders = 32

	type result struct {
		ownerID int64
		claimed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, contenders)
	var wg sync.WaitGroup
	wg.Add(contenders)
	for i := 1; i <= contenders; i++ {
		accountID := int64(i)
		go func() {
			defer wg.Done()
			<-start
			ownerID, claimed, err := cache.ClaimSessionAccount(ctx, 9, "concurrent-owner", accountID, time.Minute)
			results <- result{ownerID: ownerID, claimed: claimed, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var canonicalOwner int64
	claimCount := 0
	for got := range results {
		require.NoError(t, got.err)
		if canonicalOwner == 0 {
			canonicalOwner = got.ownerID
		}
		require.Equal(t, canonicalOwner, got.ownerID)
		if got.claimed {
			claimCount++
		}
	}
	require.NotZero(t, canonicalOwner)
	require.Equal(t, 1, claimCount)
}

func TestGatewayCacheSessionOwnerRollbackToken(t *testing.T) {
	cache, server := newSessionOwnerTestCache(t)
	rollbackCache, ok := cache.(service.OpenAISessionOwnerRollbackCache)
	require.True(t, ok)
	ctx := context.Background()
	const groupID = int64(11)

	t.Run("rolls back an unconsumed claim", func(t *testing.T) {
		const sessionHash = "rollback-claim"
		ownerID, claimed, err := rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			101,
			time.Minute,
			"claim-token",
		)
		require.NoError(t, err)
		require.True(t, claimed)
		require.Equal(t, int64(101), ownerID)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			101,
			0,
			time.Minute,
			"claim-token",
		)
		require.NoError(t, err)
		require.True(t, rolledBack)
		require.False(t, server.Exists(buildSessionKey(groupID, sessionHash)))
	})

	t.Run("keeps a claim observed by another request", func(t *testing.T) {
		const sessionHash = "consumed-claim"
		_, claimed, err := rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			101,
			time.Minute,
			"consumed-claim-token",
		)
		require.NoError(t, err)
		require.True(t, claimed)

		ownerID, claimed, err := cache.ClaimSessionAccount(ctx, groupID, sessionHash, 202, time.Minute)
		require.NoError(t, err)
		require.False(t, claimed)
		require.Equal(t, int64(101), ownerID)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			101,
			0,
			time.Minute,
			"consumed-claim-token",
		)
		require.NoError(t, err)
		require.False(t, rolledBack)
		value, err := server.Get(buildSessionKey(groupID, sessionHash))
		require.NoError(t, err)
		require.Equal(t, "101", value)
	})

	t.Run("keeps a claim rollbackable while another selection is still provisional", func(t *testing.T) {
		const sessionHash = "provisional-observer"
		_, claimed, err := rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			201,
			time.Minute,
			"first-token",
		)
		require.NoError(t, err)
		require.True(t, claimed)

		ownerID, claimed, err := rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			202,
			time.Minute,
			"second-token",
		)
		require.NoError(t, err)
		require.False(t, claimed)
		require.Equal(t, int64(201), ownerID)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			201,
			0,
			time.Minute,
			"first-token",
		)
		require.NoError(t, err)
		require.True(t, rolledBack)
		require.False(t, server.Exists(buildSessionKey(groupID, sessionHash)))
	})

	t.Run("restores an unconsumed migration", func(t *testing.T) {
		const sessionHash = "rollback-migration"
		_, _, err := cache.ClaimSessionAccount(ctx, groupID, sessionHash, 301, time.Minute)
		require.NoError(t, err)
		swapped, err := rollbackCache.CompareAndSwapSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			301,
			302,
			time.Minute,
			"migration-token",
		)
		require.NoError(t, err)
		require.True(t, swapped)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			302,
			301,
			time.Minute,
			"migration-token",
		)
		require.NoError(t, err)
		require.True(t, rolledBack)
		value, err := server.Get(buildSessionKey(groupID, sessionHash))
		require.NoError(t, err)
		require.Equal(t, "301", value)
	})

	t.Run("keeps a migration whose owner ttl was refreshed", func(t *testing.T) {
		const sessionHash = "consumed-migration"
		_, _, err := cache.ClaimSessionAccount(ctx, groupID, sessionHash, 401, time.Minute)
		require.NoError(t, err)
		swapped, err := rollbackCache.CompareAndSwapSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			401,
			402,
			time.Minute,
			"consumed-migration-token",
		)
		require.NoError(t, err)
		require.True(t, swapped)
		refreshed, err := cache.RefreshSessionTTLIfOwner(ctx, groupID, sessionHash, 402, 2*time.Minute)
		require.NoError(t, err)
		require.True(t, refreshed)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			402,
			401,
			time.Minute,
			"consumed-migration-token",
		)
		require.NoError(t, err)
		require.False(t, rolledBack)
		value, err := server.Get(buildSessionKey(groupID, sessionHash))
		require.NoError(t, err)
		require.Equal(t, "402", value)
	})

	t.Run("token mismatch does not consume a valid rollback token", func(t *testing.T) {
		const sessionHash = "rollback-token-mismatch"
		_, claimed, err := rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			501,
			time.Minute,
			"valid-token",
		)
		require.NoError(t, err)
		require.True(t, claimed)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			501,
			0,
			time.Minute,
			"wrong-token",
		)
		require.NoError(t, err)
		require.False(t, rolledBack)

		rolledBack, err = rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			501,
			0,
			time.Minute,
			"valid-token",
		)
		require.NoError(t, err)
		require.True(t, rolledBack)
	})

	t.Run("failed guarded migration does not consume another selection token", func(t *testing.T) {
		const sessionHash = "guarded-cas-mismatch"
		_, claimed, err := rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			551,
			time.Minute,
			"claim-token",
		)
		require.NoError(t, err)
		require.True(t, claimed)

		swapped, err := rollbackCache.CompareAndSwapSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			999,
			552,
			time.Minute,
			"migration-token",
		)
		require.NoError(t, err)
		require.False(t, swapped)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			551,
			0,
			time.Minute,
			"claim-token",
		)
		require.NoError(t, err)
		require.True(t, rolledBack)
	})

	t.Run("ordinary compare and swap consumes a migration rollback token", func(t *testing.T) {
		const sessionHash = "cas-consumes-token"
		_, _, err := cache.ClaimSessionAccount(ctx, groupID, sessionHash, 601, time.Minute)
		require.NoError(t, err)
		swapped, err := rollbackCache.CompareAndSwapSessionAccountWithRollbackToken(
			ctx,
			groupID,
			sessionHash,
			601,
			602,
			time.Minute,
			"cas-token",
		)
		require.NoError(t, err)
		require.True(t, swapped)

		swapped, err = cache.CompareAndSwapSessionAccount(ctx, groupID, sessionHash, 602, 602, 2*time.Minute)
		require.NoError(t, err)
		require.True(t, swapped)

		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			sessionHash,
			602,
			601,
			time.Minute,
			"cas-token",
		)
		require.NoError(t, err)
		require.False(t, rolledBack)
		value, err := server.Get(buildSessionKey(groupID, sessionHash))
		require.NoError(t, err)
		require.Equal(t, "602", value)
	})

	t.Run("ordinary set and refresh consume claim rollback tokens", func(t *testing.T) {
		gatewayCache, ok := cache.(service.GatewayCache)
		require.True(t, ok)

		const setSessionHash = "set-consumes-token"
		_, claimed, err := rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			setSessionHash,
			701,
			time.Minute,
			"set-token",
		)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NoError(t, gatewayCache.SetSessionAccountID(ctx, groupID, setSessionHash, 701, time.Minute))
		rolledBack, err := rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			setSessionHash,
			701,
			0,
			time.Minute,
			"set-token",
		)
		require.NoError(t, err)
		require.False(t, rolledBack)

		const refreshSessionHash = "refresh-consumes-token"
		_, claimed, err = rollbackCache.ClaimSessionAccountWithRollbackToken(
			ctx,
			groupID,
			refreshSessionHash,
			702,
			time.Minute,
			"refresh-token",
		)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NoError(t, gatewayCache.RefreshSessionTTL(ctx, groupID, refreshSessionHash, 2*time.Minute))
		rolledBack, err = rollbackCache.RollbackSessionAccount(
			ctx,
			groupID,
			refreshSessionHash,
			702,
			0,
			time.Minute,
			"refresh-token",
		)
		require.NoError(t, err)
		require.False(t, rolledBack)
	})
}
