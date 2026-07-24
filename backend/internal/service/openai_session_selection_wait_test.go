package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type openAIWaitRecheckConcurrencyCache struct {
	ConcurrencyCache
	acquireLimits []int
	releaseCount  int
	acquireResult bool
}

func (c *openAIWaitRecheckConcurrencyCache) AcquireAccountSlot(
	_ context.Context,
	_ int64,
	maxConcurrency int,
	_ string,
) (bool, error) {
	c.acquireLimits = append(c.acquireLimits, maxConcurrency)
	return c.acquireResult, nil
}

func (c *openAIWaitRecheckConcurrencyCache) ReleaseAccountSlot(
	_ context.Context,
	_ int64,
	_ string,
) error {
	c.releaseCount++
	return nil
}

func openAIWaitRecheckAccount(id int64, concurrency, reserve int) Account {
	return Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: concurrency,
		Extra: map[string]any{
			AccountExtraAffinityConcurrencyReserve: reserve,
		},
	}
}

func TestFinalizeAcquiredOpenAISelection_RevalidatesWaitPlanCapacity(t *testing.T) {
	const accountID = int64(48001)
	stale := openAIWaitRecheckAccount(accountID, 4, 1)
	fresh := openAIWaitRecheckAccount(accountID, 2, 1)
	concurrencyCache := &openAIWaitRecheckConcurrencyCache{acquireResult: true}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{fresh}},
		cache:              &stubGatewayCache{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	initialReleaseCount := 0
	req := OpenAIAccountScheduleRequest{
		Platform:    PlatformOpenAI,
		SessionHash: "wait-plan-capacity",
	}
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:     &stale,
		Acquired:    true,
		ReleaseFunc: func() { initialReleaseCount++ },
		WaitPlan: &AccountWaitPlan{
			AccountID:      accountID,
			MaxConcurrency: stale.GeneralConcurrencyLimit(),
			Timeout:        time.Second,
			MaxWaiting:     2,
		},
	}, req)

	finalized, err := svc.FinalizeAcquiredOpenAISelection(
		context.Background(),
		nil,
		req.SessionHash,
		selection,
	)

	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.True(t, finalized.Acquired)
	require.Nil(t, finalized.WaitPlan)
	require.Equal(t, 2, finalized.Account.Concurrency)
	require.Equal(t, 1, initialReleaseCount)
	require.Equal(t, []int{fresh.GeneralConcurrencyLimit()}, concurrencyCache.acquireLimits)

	finalized.ReleaseFunc()
	require.Equal(t, 1, concurrencyCache.releaseCount)
}

func TestFinalizeAcquiredOpenAISelection_RejectsWaitTargetThatBecameIneligible(t *testing.T) {
	const accountID = int64(48002)
	stale := openAIWaitRecheckAccount(accountID, 4, 1)
	fresh := stale
	fresh.Schedulable = false
	concurrencyCache := &openAIWaitRecheckConcurrencyCache{acquireResult: true}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{fresh}},
		cache:              &stubGatewayCache{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	initialReleaseCount := 0
	req := OpenAIAccountScheduleRequest{
		Platform:    PlatformOpenAI,
		SessionHash: "wait-plan-ineligible",
	}
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:     &stale,
		Acquired:    true,
		ReleaseFunc: func() { initialReleaseCount++ },
		WaitPlan: &AccountWaitPlan{
			AccountID:      accountID,
			MaxConcurrency: stale.GeneralConcurrencyLimit(),
			Timeout:        time.Second,
			MaxWaiting:     2,
		},
	}, req)

	finalized, err := svc.FinalizeAcquiredOpenAISelection(
		context.Background(),
		nil,
		req.SessionHash,
		selection,
	)

	require.ErrorContains(t, err, "wait_target_became_ineligible")
	require.Nil(t, finalized)
	require.Equal(t, 1, initialReleaseCount)
	require.Empty(t, concurrencyCache.acquireLimits)
}
