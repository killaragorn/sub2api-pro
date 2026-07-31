package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheKeywordSessionBlockClaimIsAtomicAndExpires(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	store, ok := NewGatewayCache(client).(service.KeywordSessionBlockStore)
	require.True(t, ok)
	otherStore, ok := NewGatewayCache(client).(service.KeywordSessionBlockStore)
	require.True(t, ok)
	ctx := context.Background()

	claimed, err := store.ClaimKeywordSessionBlocked(ctx, "policy-session", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = otherStore.ClaimKeywordSessionBlocked(ctx, "policy-session", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	blocked, err := otherStore.IsKeywordSessionBlocked(ctx, "policy-session")
	require.NoError(t, err)
	require.True(t, blocked)

	redisServer.FastForward(time.Minute + time.Second)
	blocked, err = store.IsKeywordSessionBlocked(ctx, "policy-session")
	require.NoError(t, err)
	require.False(t, blocked)
}
