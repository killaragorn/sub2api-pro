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
