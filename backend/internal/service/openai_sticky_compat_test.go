package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type legacyDualWriteFailureCache struct {
	stubGatewayCache
}

func (c *legacyDualWriteFailureCache) SetSessionAccountID(
	ctx context.Context,
	groupID int64,
	sessionHash string,
	accountID int64,
	ttl time.Duration,
) error {
	if sessionHash == "openai:legacy-hash" {
		return errors.New("legacy write failed")
	}
	return c.stubGatewayCache.SetSessionAccountID(ctx, groupID, sessionHash, accountID, ttl)
}

func TestGetStickySessionAccountID_FallbackToLegacyKey(t *testing.T) {
	beforeFallbackTotal, beforeFallbackHit, _ := openAIStickyCompatStats()

	cache := &stubGatewayCache{
		sessionBindings: map[string]int64{
			"openai:legacy-hash": 42,
		},
	}
	svc := &OpenAIGatewayService{
		cache: cache,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					SessionHashReadOldFallback: true,
				},
			},
		},
	}

	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")
	accountID, err := svc.getStickySessionAccountID(ctx, nil, "new-hash")
	require.NoError(t, err)
	require.Equal(t, int64(42), accountID)

	afterFallbackTotal, afterFallbackHit, _ := openAIStickyCompatStats()
	require.Equal(t, beforeFallbackTotal+1, afterFallbackTotal)
	require.Equal(t, beforeFallbackHit+1, afterFallbackHit)
}

func TestSetStickySessionAccountID_DualWriteOldEnabled(t *testing.T) {
	_, _, beforeDualWriteTotal := openAIStickyCompatStats()

	cache := &stubGatewayCache{sessionBindings: map[string]int64{}}
	svc := &OpenAIGatewayService{
		cache: cache,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					SessionHashDualWriteOld: true,
				},
			},
		},
	}

	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")
	err := svc.setStickySessionAccountID(ctx, nil, "new-hash", 9, openaiStickySessionTTL)
	require.NoError(t, err)
	require.Equal(t, int64(9), cache.sessionBindings["openai:new-hash"])
	require.Equal(t, int64(9), cache.sessionBindings["openai:legacy-hash"])

	_, _, afterDualWriteTotal := openAIStickyCompatStats()
	require.Equal(t, beforeDualWriteTotal+1, afterDualWriteTotal)
}

func TestSetStickySessionAccountID_DualWriteOldDisabled(t *testing.T) {
	cache := &stubGatewayCache{sessionBindings: map[string]int64{}}
	svc := &OpenAIGatewayService{
		cache: cache,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					SessionHashDualWriteOld: false,
				},
			},
		},
	}

	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")
	err := svc.setStickySessionAccountID(ctx, nil, "new-hash", 9, openaiStickySessionTTL)
	require.NoError(t, err)
	require.Equal(t, int64(9), cache.sessionBindings["openai:new-hash"])
	_, exists := cache.sessionBindings["openai:legacy-hash"]
	require.False(t, exists)
}

func TestClaimStickySessionAccountID_LegacyDualWriteFailureKeepsCanonicalClaim(t *testing.T) {
	before := SnapshotOpenAICompatibilityFallbackMetrics()
	cache := &legacyDualWriteFailureCache{
		stubGatewayCache: stubGatewayCache{sessionBindings: map[string]int64{}},
	}
	svc := &OpenAIGatewayService{
		cache: cache,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					SessionHashDualWriteOld: true,
				},
			},
		},
	}

	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")
	ownerID, claimed, err := svc.claimStickySessionAccountID(ctx, nil, "new-hash", 19, openaiStickySessionTTL)

	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, int64(19), ownerID)
	require.Equal(t, int64(19), cache.sessionBindings["openai:new-hash"])
	_, legacyExists := cache.sessionBindings["openai:legacy-hash"]
	require.False(t, legacyExists)

	after := SnapshotOpenAICompatibilityFallbackMetrics()
	require.Equal(t, before.SessionHashLegacyDualWriteError+1, after.SessionHashLegacyDualWriteError)
	require.Equal(t, before.SessionHashLegacyDualWriteTotal, after.SessionHashLegacyDualWriteTotal)
}

func TestRejectAcquiredOpenAISelection_RollsBackOnlyOwnedLegacyWrite(t *testing.T) {
	const (
		primaryHash = "new-hash"
		legacyHash  = "legacy-hash"
		accountID   = int64(29)
	)
	ctx := withOpenAILegacySessionHash(context.Background(), legacyHash)
	newService := func(cache *rollbackAwareGatewayCache) *OpenAIGatewayService {
		return &OpenAIGatewayService{
			cache: cache,
			cfg: &config.Config{
				Gateway: config.GatewayConfig{
					OpenAIWS: config.GatewayOpenAIWSConfig{
						SessionHashDualWriteOld: true,
					},
				},
			},
		}
	}

	t.Run("removes a legacy key created by the provisional claim", func(t *testing.T) {
		cache := &rollbackAwareGatewayCache{}
		svc := newService(cache)
		selection, err := svc.settleAcquiredOpenAISelection(ctx, OpenAIAccountScheduleRequest{
			SessionHash: primaryHash,
		}, &AccountSelectionResult{
			Account:     &Account{ID: accountID},
			Acquired:    true,
			ReleaseFunc: func() {},
		})
		require.NoError(t, err)
		require.True(t, selection.stickyBindingLegacyClaimed)

		require.NoError(t, svc.RejectAcquiredOpenAISelection(ctx, nil, primaryHash, selection))
		require.NotContains(t, cache.sessionBindings, "openai:"+primaryHash)
		require.NotContains(t, cache.sessionBindings, "openai:"+legacyHash)
	})

	t.Run("preserves a legacy key that existed before the provisional claim", func(t *testing.T) {
		cache := &rollbackAwareGatewayCache{
			stubGatewayCache: stubGatewayCache{
				sessionBindings: map[string]int64{"openai:" + legacyHash: accountID},
			},
		}
		svc := newService(cache)
		selection, err := svc.settleAcquiredOpenAISelection(ctx, OpenAIAccountScheduleRequest{
			SessionHash: primaryHash,
		}, &AccountSelectionResult{
			Account:     &Account{ID: accountID},
			Acquired:    true,
			ReleaseFunc: func() {},
		})
		require.NoError(t, err)
		require.False(t, selection.stickyBindingLegacyClaimed)

		require.NoError(t, svc.RejectAcquiredOpenAISelection(ctx, nil, primaryHash, selection))
		require.NotContains(t, cache.sessionBindings, "openai:"+primaryHash)
		require.Equal(t, accountID, cache.sessionBindings["openai:"+legacyHash])
	})
}

func TestSnapshotOpenAICompatibilityFallbackMetrics(t *testing.T) {
	before := SnapshotOpenAICompatibilityFallbackMetrics()

	ctx := context.WithValue(context.Background(), ctxkey.ThinkingEnabled, true)
	_, _ = ThinkingEnabledFromContext(ctx)

	after := SnapshotOpenAICompatibilityFallbackMetrics()
	require.GreaterOrEqual(t, after.MetadataLegacyFallbackTotal, before.MetadataLegacyFallbackTotal+1)
	require.GreaterOrEqual(t, after.MetadataLegacyFallbackThinkingEnabledTotal, before.MetadataLegacyFallbackThinkingEnabledTotal+1)
}
