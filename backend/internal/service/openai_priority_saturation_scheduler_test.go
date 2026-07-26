package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type prioritySaturationAcquireAttempt struct {
	accountID int64
	limit     int
}

type prioritySaturationConcurrencyCache struct {
	ConcurrencyCache
	mu       sync.Mutex
	active   map[int64]int
	waiting  map[int64]int
	requests map[string]int64
	attempts []prioritySaturationAcquireAttempt
	released []int64
}

func (c *prioritySaturationConcurrencyCache) AcquireAccountSlot(_ context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		c.active = make(map[int64]int)
	}
	if c.requests == nil {
		c.requests = make(map[string]int64)
	}
	c.attempts = append(c.attempts, prioritySaturationAcquireAttempt{accountID: accountID, limit: maxConcurrency})
	if maxConcurrency > 0 && c.active[accountID] >= maxConcurrency {
		return false, nil
	}
	c.active[accountID]++
	c.requests[requestID] = accountID
	return true, nil
}

func (c *prioritySaturationConcurrencyCache) ReleaseAccountSlot(_ context.Context, accountID int64, requestID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if heldAccountID, ok := c.requests[requestID]; ok {
		delete(c.requests, requestID)
		if c.active[heldAccountID] > 0 {
			c.active[heldAccountID]--
		}
	}
	c.released = append(c.released, accountID)
	return nil
}

func (c *prioritySaturationConcurrencyCache) GetAccountConcurrency(_ context.Context, accountID int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active[accountID], nil
}

func (c *prioritySaturationConcurrencyCache) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		result[accountID] = c.active[accountID]
	}
	return result, nil
}

func (c *prioritySaturationConcurrencyCache) GetAccountWaitingCount(_ context.Context, accountID int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waiting[accountID], nil
}

func (c *prioritySaturationConcurrencyCache) GetAccountsLoadBatch(_ context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make(map[int64]*AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		currentConcurrency := c.active[account.ID]
		loadRate := 0
		if account.MaxConcurrency > 0 {
			loadRate = currentConcurrency * 100 / account.MaxConcurrency
		}
		result[account.ID] = &AccountLoadInfo{
			AccountID:          account.ID,
			CurrentConcurrency: currentConcurrency,
			LoadRate:           loadRate,
		}
	}
	return result, nil
}

type prioritySaturationSessionCache struct {
	schedulerTestGatewayCache
	mu sync.Mutex
}

type rotatingPrioritySaturationSessionCache struct {
	*prioritySaturationSessionCache
	owners []int64
	next   int
}

type stalePriorityWaitAccountRepo struct {
	schedulerTestOpenAIAccountRepo
	staleAccountID int64
}

func (r stalePriorityWaitAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	account, err := r.schedulerTestOpenAIAccountRepo.GetByID(ctx, id)
	if err != nil || account == nil || id != r.staleAccountID {
		return account, err
	}
	stale := *account
	stale.Schedulable = false
	return &stale, nil
}

func (c *rotatingPrioritySaturationSessionCache) ClaimSessionAccount(_ context.Context, _ int64, _ string, _ int64, _ time.Duration) (int64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ownerID := c.owners[c.next%len(c.owners)]
	c.next++
	return ownerID, false, nil
}

func (c *prioritySaturationSessionCache) ClaimSessionAccount(_ context.Context, _ int64, sessionHash string, accountID int64, _ time.Duration) (int64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionBindings == nil {
		c.sessionBindings = make(map[string]int64)
	}
	if ownerID := c.sessionBindings[sessionHash]; ownerID > 0 {
		return ownerID, false, nil
	}
	c.sessionBindings[sessionHash] = accountID
	return accountID, true, nil
}

func (c *prioritySaturationSessionCache) CompareAndSwapSessionAccount(_ context.Context, _ int64, sessionHash string, oldAccountID, newAccountID int64, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionBindings[sessionHash] != oldAccountID {
		return false, nil
	}
	c.sessionBindings[sessionHash] = newAccountID
	return true, nil
}

func (c *prioritySaturationSessionCache) RefreshSessionTTLIfOwner(_ context.Context, _ int64, sessionHash string, accountID int64, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionBindings[sessionHash] == accountID, nil
}

func (c *prioritySaturationSessionCache) DeleteSessionAccountIfOwner(_ context.Context, _ int64, sessionHash string, accountID int64) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionBindings[sessionHash] != accountID {
		return false, nil
	}
	delete(c.sessionBindings, sessionHash)
	return true, nil
}

func prioritySaturationTestAccount(id int64, priority, concurrency, reserve int) Account {
	extra := map[string]any{}
	if reserve > 0 {
		extra[AccountExtraAffinityConcurrencyReserve] = reserve
	}
	return Account{
		ID:          id,
		Name:        "priority-saturation-test",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: concurrency,
		Priority:    priority,
		Extra:       extra,
	}
}

func newPrioritySaturationTestScheduler(
	accounts []Account,
	active map[int64]int,
	bindings map[string]int64,
) (*prioritySaturationOpenAIAccountScheduler, *prioritySaturationConcurrencyCache, *prioritySaturationSessionCache) {
	concurrencyCache := &prioritySaturationConcurrencyCache{active: active}
	sessionCache := &prioritySaturationSessionCache{
		schedulerTestGatewayCache: schedulerTestGatewayCache{sessionBindings: bindings},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              sessionCache,
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}
	scheduler, ok := newPrioritySaturationOpenAIAccountScheduler(svc, nil).(*prioritySaturationOpenAIAccountScheduler)
	if !ok {
		panic("priority saturation scheduler constructor returned an unexpected type")
	}
	return scheduler, concurrencyCache, sessionCache
}

func prioritySaturationRequest(sessionHash string, stickyAccountID int64) OpenAIAccountScheduleRequest {
	return OpenAIAccountScheduleRequest{
		Platform:          PlatformOpenAI,
		SessionHash:       sessionHash,
		StickyAccountID:   stickyAccountID,
		RequiredTransport: OpenAIUpstreamTransportHTTPSSE,
	}
}

func TestPrioritySaturationScheduler_NewSessionsUsePriorityThenIDAndGeneralLimit(t *testing.T) {
	accounts := []Account{
		prioritySaturationTestAccount(20, 1, 3, 1),
		prioritySaturationTestAccount(10, 1, 3, 1),
		prioritySaturationTestAccount(30, 2, 3, 0),
	}
	scheduler, cache, _ := newPrioritySaturationTestScheduler(accounts, map[int64]int{10: 2}, nil)

	selection, decision, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.True(t, selection.Acquired)
	require.Equal(t, int64(20), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
	require.Equal(t, []prioritySaturationAcquireAttempt{
		{accountID: 10, limit: 2},
		{accountID: 20, limit: 2},
	}, cache.attempts)
	selection.ReleaseFunc()
}

func TestPrioritySaturationScheduler_FillsAccountsInPriorityThenIDOrder(t *testing.T) {
	accounts := []Account{
		prioritySaturationTestAccount(30, 2, 2, 0),
		prioritySaturationTestAccount(20, 1, 3, 0),
		prioritySaturationTestAccount(10, 1, 4, 1),
	}
	scheduler, _, _ := newPrioritySaturationTestScheduler(accounts, nil, nil)

	wantAccountIDs := []int64{10, 10, 10, 20, 20, 20, 30, 30}
	selections := make([]*AccountSelectionResult, 0, len(wantAccountIDs))
	for _, wantAccountID := range wantAccountIDs {
		selection, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.True(t, selection.Acquired)
		require.Equal(t, wantAccountID, selection.Account.ID)
		selections = append(selections, selection)
	}

	waiting, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
	require.NoError(t, err)
	require.False(t, waiting.Acquired)
	require.NotNil(t, waiting.WaitPlan)
	require.Equal(t, int64(10), waiting.WaitPlan.AccountID)
	require.Equal(t, 3, waiting.WaitPlan.MaxConcurrency)

	for _, selection := range selections {
		selection.ReleaseFunc()
	}
}

func TestPrioritySaturationScheduler_RefillsLeadingAccountAfterRelease(t *testing.T) {
	accounts := []Account{
		prioritySaturationTestAccount(10, 1, 2, 0),
		prioritySaturationTestAccount(20, 2, 2, 0),
		prioritySaturationTestAccount(30, 3, 2, 0),
	}
	scheduler, _, _ := newPrioritySaturationTestScheduler(accounts, nil, nil)

	selections := make([]*AccountSelectionResult, 0, 3)
	for _, wantAccountID := range []int64{10, 10, 20} {
		selection, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
		require.NoError(t, err)
		require.True(t, selection.Acquired)
		require.Equal(t, wantAccountID, selection.Account.ID)
		selections = append(selections, selection)
	}

	selections[0].ReleaseFunc()
	refill, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
	require.NoError(t, err)
	require.True(t, refill.Acquired)
	require.Equal(t, int64(10), refill.Account.ID)

	refill.ReleaseFunc()
	for _, selection := range selections[1:] {
		selection.ReleaseFunc()
	}
}

func TestPrioritySaturationScheduler_StableOrderIgnoresDynamicSignals(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-time.Hour)
	expensive := 9.0
	cheap := 0.1
	accounts := []Account{
		prioritySaturationTestAccount(20, 2, 3, 0),
		prioritySaturationTestAccount(10, 1, 3, 0),
	}
	accounts[0].LastUsedAt = &earlier
	accounts[0].RateMultiplier = &cheap
	accounts[1].LastUsedAt = &now
	accounts[1].RateMultiplier = &expensive

	scheduler, cache, _ := newPrioritySaturationTestScheduler(
		accounts,
		map[int64]int{10: 1},
		nil,
	)
	cache.waiting = map[int64]int{10: 100, 20: 0}

	slowTTFT := 10_000
	fastTTFT := 1
	for range 8 {
		scheduler.ReportResult(10, false, &slowTTFT)
		scheduler.ReportResult(20, true, &fastTTFT)
	}

	selection, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, int64(10), selection.Account.ID)
	require.Equal(t, []prioritySaturationAcquireAttempt{{accountID: 10, limit: 3}}, cache.attempts)
	selection.ReleaseFunc()
}

func TestPrioritySaturationScheduler_ConcurrentNewSessionsRespectAtomicOrder(t *testing.T) {
	accounts := []Account{
		prioritySaturationTestAccount(10, 1, 35, 0),
		prioritySaturationTestAccount(20, 2, 35, 0),
		prioritySaturationTestAccount(30, 3, 35, 0),
	}
	scheduler, cache, _ := newPrioritySaturationTestScheduler(accounts, nil, nil)

	const requests = 100
	start := make(chan struct{})
	results := make(chan *AccountSelectionResult, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			<-start
			selection, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
			if err != nil {
				errs <- err
				return
			}
			results <- selection
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	selections := make([]*AccountSelectionResult, 0, requests)
	selectedCounts := make(map[int64]int)
	for selection := range results {
		require.NotNil(t, selection)
		require.True(t, selection.Acquired)
		require.NotNil(t, selection.Account)
		selectedCounts[selection.Account.ID]++
		selections = append(selections, selection)
	}
	require.Len(t, selections, requests)
	require.Equal(t, 35, selectedCounts[10])
	require.Equal(t, 35, selectedCounts[20])
	require.Equal(t, 30, selectedCounts[30])

	cache.mu.Lock()
	require.Equal(t, 35, cache.active[10])
	require.Equal(t, 35, cache.active[20])
	require.Equal(t, 30, cache.active[30])
	cache.mu.Unlock()

	for _, selection := range selections {
		selection.ReleaseFunc()
	}
	cache.mu.Lock()
	require.Zero(t, cache.active[10])
	require.Zero(t, cache.active[20])
	require.Zero(t, cache.active[30])
	cache.mu.Unlock()
}

func TestPrioritySaturationScheduler_ConcurrentGeneralTrafficCannotConsumeReserve(t *testing.T) {
	accounts := []Account{
		prioritySaturationTestAccount(10, 1, 35, 5),
		prioritySaturationTestAccount(20, 2, 35, 5),
		prioritySaturationTestAccount(30, 3, 35, 5),
	}
	scheduler, cache, _ := newPrioritySaturationTestScheduler(accounts, nil, nil)

	const requests = 100
	start := make(chan struct{})
	results := make(chan *AccountSelectionResult, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			<-start
			selection, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
			if err != nil {
				errs <- err
				return
			}
			results <- selection
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	acquired := make([]*AccountSelectionResult, 0, 90)
	waiting := 0
	for selection := range results {
		require.NotNil(t, selection)
		if selection.Acquired {
			acquired = append(acquired, selection)
			continue
		}
		require.NotNil(t, selection.WaitPlan)
		require.Equal(t, accounts[0].ID, selection.WaitPlan.AccountID)
		require.Equal(t, accounts[0].GeneralConcurrencyLimit(), selection.WaitPlan.MaxConcurrency)
		waiting++
	}
	require.Len(t, acquired, 90)
	require.Equal(t, 10, waiting)

	cache.mu.Lock()
	require.Equal(t, 30, cache.active[10])
	require.Equal(t, 30, cache.active[20])
	require.Equal(t, 30, cache.active[30])
	cache.mu.Unlock()

	for _, selection := range acquired {
		selection.ReleaseFunc()
	}
}

func TestPrioritySaturationScheduler_ConcurrentGeneralAndAffinityRespectBothLimits(t *testing.T) {
	account := prioritySaturationTestAccount(10, 1, 35, 5)
	scheduler, cache, _ := newPrioritySaturationTestScheduler(
		[]Account{account},
		map[int64]int{account.ID: 29},
		map[string]int64{"openai:mixed-owner": account.ID},
	)

	const requestsPerClass = 10
	start := make(chan struct{})
	results := make(chan *AccountSelectionResult, requestsPerClass*2)
	errs := make(chan error, requestsPerClass*2)
	var wg sync.WaitGroup
	run := func(req OpenAIAccountScheduleRequest) {
		defer wg.Done()
		<-start
		selection, _, err := scheduler.Select(context.Background(), req)
		if err != nil {
			errs <- err
			return
		}
		results <- selection
	}
	wg.Add(requestsPerClass * 2)
	for range requestsPerClass {
		go run(prioritySaturationRequest("", 0))
		go run(prioritySaturationRequest("mixed-owner", account.ID))
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	generalAcquired := 0
	affinityAcquired := 0
	acquired := make([]*AccountSelectionResult, 0, account.Concurrency-29)
	for selection := range results {
		require.NotNil(t, selection)
		if !selection.Acquired {
			require.NotNil(t, selection.WaitPlan)
			continue
		}
		acquired = append(acquired, selection)
		if selection.SessionOwnerID == account.ID {
			affinityAcquired++
		} else {
			generalAcquired++
		}
	}

	require.LessOrEqual(t, generalAcquired, 1)
	require.Equal(t, account.Concurrency-29, generalAcquired+affinityAcquired)
	cache.mu.Lock()
	require.Equal(t, account.Concurrency, cache.active[account.ID])
	cache.mu.Unlock()

	for _, selection := range acquired {
		selection.ReleaseFunc()
	}
	cache.mu.Lock()
	require.Equal(t, 29, cache.active[account.ID])
	cache.mu.Unlock()
}

func TestPrioritySaturationScheduler_EstablishedAffinityUsesReservedCapacity(t *testing.T) {
	account := prioritySaturationTestAccount(11, 1, 3, 1)
	scheduler, cache, _ := newPrioritySaturationTestScheduler(
		[]Account{account},
		map[int64]int{account.ID: 2},
		map[string]int64{"openai:sticky": account.ID},
	)

	selection, decision, err := scheduler.Select(context.Background(), prioritySaturationRequest("sticky", account.ID))
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, account.ID, selection.Account.ID)
	require.True(t, decision.StickySessionHit)
	require.True(t, decision.AffinityReserveHit)
	require.Equal(t, 2, decision.GeneralLimit)
	require.Equal(t, 3, decision.HardLimit)
	require.Equal(t, 1, decision.AffinityReserve)
	require.Equal(t, []prioritySaturationAcquireAttempt{{accountID: account.ID, limit: 3}}, cache.attempts)
	require.Equal(t, int64(1), scheduler.SnapshotMetrics().AffinityReserveHitTotal)
	selection.ReleaseFunc()
}

func TestPrioritySaturationScheduler_PreviousResponseAffinityUsesReservedCapacity(t *testing.T) {
	preferred := prioritySaturationTestAccount(22, 2, 3, 1)
	preferred.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
	lowerPriority := prioritySaturationTestAccount(11, 1, 3, 0)
	scheduler, cache, _ := newPrioritySaturationTestScheduler(
		[]Account{lowerPriority, preferred},
		map[int64]int{preferred.ID: preferred.GeneralConcurrencyLimit()},
		nil,
	)
	scheduler.base.service.cfg = newSchedulerTestOpenAIWSV2Config()
	ctx := context.Background()
	require.NoError(t, scheduler.base.service.getOpenAIWSStateStore().BindResponseAccount(
		ctx,
		0,
		"resp-priority-affinity",
		preferred.ID,
		time.Hour,
	))

	req := prioritySaturationRequest("", 0)
	req.PreviousResponseID = "resp-priority-affinity"
	req.PreviousResponseCanMove = false
	selection, decision, err := scheduler.Select(ctx, req)

	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, preferred.ID, selection.Account.ID)
	require.True(t, decision.StickyPreviousHit)
	require.Equal(t, []prioritySaturationAcquireAttempt{{accountID: preferred.ID, limit: preferred.Concurrency}}, cache.attempts)
	selection.ReleaseFunc()
}

func TestPrioritySaturationScheduler_FullAffinityTemporarilyOverflowsWithoutRebinding(t *testing.T) {
	owner := prioritySaturationTestAccount(11, 1, 3, 1)
	overflow := prioritySaturationTestAccount(22, 2, 4, 1)
	scheduler, cache, sessionCache := newPrioritySaturationTestScheduler(
		[]Account{owner, overflow},
		map[int64]int{owner.ID: 3},
		map[string]int64{"openai:sticky": owner.ID},
	)

	req := prioritySaturationRequest("sticky", owner.ID)
	req.CanTemporarilyOverflow = true
	selection, decision, err := scheduler.Select(context.Background(), req)
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, overflow.ID, selection.Account.ID)
	require.False(t, decision.StickySessionHit)
	require.True(t, decision.TemporaryOverflow)
	require.Equal(t, owner.ID, sessionCache.sessionBindings["openai:sticky"])
	require.Equal(t, []prioritySaturationAcquireAttempt{
		{accountID: owner.ID, limit: 3},
		{accountID: overflow.ID, limit: 3},
	}, cache.attempts)
	require.Equal(t, int64(1), scheduler.SnapshotMetrics().TemporaryOverflowTotal)
	selection.ReleaseFunc()
}

func TestPrioritySaturationScheduler_FullAffinityWithNoOverflowCandidateWaitsOnOwner(t *testing.T) {
	owner := prioritySaturationTestAccount(11, 1, 3, 1)
	scheduler, cache, sessionCache := newPrioritySaturationTestScheduler(
		[]Account{owner},
		map[int64]int{owner.ID: owner.Concurrency},
		map[string]int64{"openai:sticky": owner.ID},
	)

	req := prioritySaturationRequest("sticky", owner.ID)
	req.CanTemporarilyOverflow = true
	selection, decision, err := scheduler.Select(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, owner.ID, selection.Account.ID)
	require.Equal(t, owner.ID, selection.WaitPlan.AccountID)
	require.Equal(t, owner.Concurrency, selection.WaitPlan.MaxConcurrency)
	require.True(t, selection.PreserveStickyBinding)
	require.True(t, decision.AffinityWait)
	require.Equal(t, owner.ID, sessionCache.sessionBindings["openai:sticky"])
	require.Equal(t, []prioritySaturationAcquireAttempt{
		{accountID: owner.ID, limit: owner.Concurrency},
	}, cache.attempts)
}

func TestPrioritySaturationScheduler_FullAffinityWaitsWhenTemporaryOverflowIsNotAllowed(t *testing.T) {
	owner := prioritySaturationTestAccount(11, 1, 3, 1)
	overflow := prioritySaturationTestAccount(22, 2, 4, 1)
	scheduler, cache, sessionCache := newPrioritySaturationTestScheduler(
		[]Account{owner, overflow},
		map[int64]int{owner.ID: 3},
		map[string]int64{"openai:sticky": owner.ID},
	)

	selection, decision, err := scheduler.Select(context.Background(), prioritySaturationRequest("sticky", owner.ID))
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.False(t, selection.Acquired)
	require.Equal(t, owner.ID, selection.Account.ID)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, owner.ID, selection.WaitPlan.AccountID)
	require.Equal(t, owner.Concurrency, selection.WaitPlan.MaxConcurrency)
	require.True(t, decision.StickySessionHit)
	require.True(t, decision.AffinityWait)
	require.Equal(t, owner.ID, sessionCache.sessionBindings["openai:sticky"])
	require.Equal(t, []prioritySaturationAcquireAttempt{
		{accountID: owner.ID, limit: owner.Concurrency},
	}, cache.attempts)
	require.Equal(t, int64(1), scheduler.SnapshotMetrics().AffinityWaitTotal)
}

func TestPrioritySaturationScheduler_ExcludedAffinityDoesNotMoveWhenTemporaryOverflowIsNotAllowed(t *testing.T) {
	owner := prioritySaturationTestAccount(11, 1, 3, 1)
	overflow := prioritySaturationTestAccount(22, 2, 4, 1)
	scheduler, cache, sessionCache := newPrioritySaturationTestScheduler(
		[]Account{owner, overflow},
		nil,
		map[string]int64{"openai:sticky": owner.ID},
	)
	req := prioritySaturationRequest("sticky", owner.ID)
	req.ExcludedIDs = map[int64]struct{}{owner.ID: {}}

	selection, decision, err := scheduler.Select(context.Background(), req)
	require.Nil(t, selection)
	require.ErrorContains(t, err, "session_affinity_unavailable")
	require.True(t, decision.AffinityRejected)
	require.Empty(t, cache.attempts)
	require.Equal(t, owner.ID, sessionCache.sessionBindings["openai:sticky"])
	require.Equal(t, int64(1), scheduler.SnapshotMetrics().AffinityRejectedTotal)
}

func TestPrioritySaturationScheduler_MissingPreviousResponseDoesNotMoveWithoutBothPermissions(t *testing.T) {
	account := prioritySaturationTestAccount(11, 1, 3, 0)
	scheduler, cache, _ := newPrioritySaturationTestScheduler([]Account{account}, nil, nil)
	req := prioritySaturationRequest("", 0)
	req.PreviousResponseID = "missing-response"
	req.PreviousResponseCanMove = true

	selection, _, err := scheduler.Select(context.Background(), req)
	require.Nil(t, selection)
	require.ErrorContains(t, err, "previous_response_affinity_missing")
	require.Empty(t, cache.attempts)
}

func TestPrioritySaturationScheduler_ClaimRaceConvergesToCanonicalOwner(t *testing.T) {
	candidate := prioritySaturationTestAccount(22, 1, 4, 1)
	owner := prioritySaturationTestAccount(11, 2, 3, 1)
	scheduler, cache, sessionCache := newPrioritySaturationTestScheduler(
		[]Account{candidate, owner},
		map[int64]int{},
		map[string]int64{"openai:race": owner.ID},
	)

	selection, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("race", 0))
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, owner.ID, selection.Account.ID)
	require.Equal(t, owner.ID, sessionCache.sessionBindings["openai:race"])
	require.Equal(t, []prioritySaturationAcquireAttempt{
		{accountID: candidate.ID, limit: 3},
		{accountID: owner.ID, limit: 3},
	}, cache.attempts)
	require.Equal(t, []int64{candidate.ID}, cache.released)
	selection.ReleaseFunc()
}

func TestPrioritySaturationScheduler_ReconciliationExhaustionReleasesCurrentProvisionalSlot(t *testing.T) {
	accounts := []Account{
		prioritySaturationTestAccount(10, 1, 2, 0),
		prioritySaturationTestAccount(20, 2, 2, 0),
		prioritySaturationTestAccount(30, 3, 2, 0),
	}
	concurrencyCache := &prioritySaturationConcurrencyCache{}
	sessionCache := &rotatingPrioritySaturationSessionCache{
		prioritySaturationSessionCache: &prioritySaturationSessionCache{
			schedulerTestGatewayCache: schedulerTestGatewayCache{},
		},
		owners: []int64{20, 30, 10},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              sessionCache,
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}
	scheduler := newPrioritySaturationOpenAIAccountScheduler(svc, nil)

	selection, _, err := scheduler.Select(context.Background(), prioritySaturationRequest("rotating-owner", 0))
	require.Nil(t, selection)
	require.ErrorContains(t, err, "owner changed too frequently")
	require.Empty(t, concurrencyCache.requests)
	for _, account := range accounts {
		require.Zero(t, concurrencyCache.active[account.ID], "account %d leaked a provisional slot", account.ID)
	}
}

func TestPrioritySaturationScheduler_AllGeneralPoolsFullWaitsOnFirstPriority(t *testing.T) {
	first := prioritySaturationTestAccount(12, 1, 3, 1)
	second := prioritySaturationTestAccount(21, 2, 4, 1)
	scheduler, cache, _ := newPrioritySaturationTestScheduler(
		[]Account{second, first},
		map[int64]int{first.ID: 2, second.ID: 3},
		nil,
	)

	selection, decision, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
	require.NoError(t, err)
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, first.ID, selection.WaitPlan.AccountID)
	require.Equal(t, 2, selection.WaitPlan.MaxConcurrency)
	require.Equal(t, []prioritySaturationAcquireAttempt{
		{accountID: first.ID, limit: 2},
		{accountID: second.ID, limit: 3},
	}, cache.attempts)
	require.Equal(t, 2, decision.GeneralRejectCount)
	require.Equal(t, int64(2), scheduler.SnapshotMetrics().GeneralRejectTotal)
}

func TestPrioritySaturationScheduler_StaleFirstCandidateWaitsOnNextValidFullAccount(t *testing.T) {
	stale := prioritySaturationTestAccount(12, 1, 3, 1)
	waitable := prioritySaturationTestAccount(21, 2, 4, 1)
	scheduler, cache, _ := newPrioritySaturationTestScheduler(
		[]Account{stale, waitable},
		map[int64]int{waitable.ID: waitable.GeneralConcurrencyLimit()},
		nil,
	)
	scheduler.base.service.accountRepo = stalePriorityWaitAccountRepo{
		schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{stale, waitable}},
		staleAccountID:                 stale.ID,
	}

	selection, decision, err := scheduler.Select(context.Background(), prioritySaturationRequest("", 0))
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, waitable.ID, selection.WaitPlan.AccountID)
	require.Equal(t, waitable.GeneralConcurrencyLimit(), selection.WaitPlan.MaxConcurrency)
	require.Equal(t, 1, decision.GeneralRejectCount)
	require.Equal(t, []int64{stale.ID}, cache.released)
}

func TestGetOpenAIAccountScheduler_PrioritySaturationIsIndependentAndWinsDefensiveConflict(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	repo := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
		SettingKeyOpenAIPrioritySaturationEnabled: "true",
		openAIAdvancedSchedulerSettingKey:         "true",
	}}
	svc := &OpenAIGatewayService{
		cfg:              &config.Config{},
		rateLimitService: &RateLimitService{settingService: NewSettingService(repo, &config.Config{})},
	}

	scheduler := svc.getOpenAIAccountScheduler(context.Background())
	require.IsType(t, &prioritySaturationOpenAIAccountScheduler{}, scheduler)
	require.True(t, svc.isOpenAIPrioritySaturationEnabled(context.Background()))
	require.False(t, svc.isOpenAIAdvancedSchedulerEnabled(context.Background()))
}

type crossPolicySessionRoleKey struct{}

type crossPolicySessionCache struct {
	*prioritySaturationSessionCache
	barrierMu     sync.Mutex
	arrived       int
	bothArrived   chan struct{}
	winnerClaimed chan struct{}
	winnerRole    string
}

func newCrossPolicySessionCache(winnerRole string) *crossPolicySessionCache {
	return &crossPolicySessionCache{
		prioritySaturationSessionCache: &prioritySaturationSessionCache{},
		bothArrived:                    make(chan struct{}),
		winnerClaimed:                  make(chan struct{}),
		winnerRole:                     winnerRole,
	}
}

func (c *crossPolicySessionCache) ClaimSessionAccount(
	ctx context.Context,
	groupID int64,
	sessionHash string,
	accountID int64,
	ttl time.Duration,
) (int64, bool, error) {
	role, _ := ctx.Value(crossPolicySessionRoleKey{}).(string)

	c.barrierMu.Lock()
	c.arrived++
	initialClaim := c.arrived <= 2
	if c.arrived == 2 {
		close(c.bothArrived)
	}
	c.barrierMu.Unlock()

	if initialClaim {
		select {
		case <-c.bothArrived:
		case <-ctx.Done():
			return 0, false, ctx.Err()
		}
		if role != c.winnerRole {
			select {
			case <-c.winnerClaimed:
			case <-ctx.Done():
				return 0, false, ctx.Err()
			}
		}
	}

	ownerID, claimed, err := c.prioritySaturationSessionCache.ClaimSessionAccount(
		ctx,
		groupID,
		sessionHash,
		accountID,
		ttl,
	)
	if initialClaim && role == c.winnerRole {
		close(c.winnerClaimed)
	}
	return ownerID, claimed, err
}

func TestOpenAIAccountSchedulers_TwoServiceInstancesConvergeOnAtomicOwner(t *testing.T) {
	for _, winnerRole := range []string{"weighted", "priority"} {
		t.Run(winnerRole+" wins initial claim", func(t *testing.T) {
			accounts := []Account{
				prioritySaturationTestAccount(10, 1, 1, 0),
				prioritySaturationTestAccount(20, 2, 1, 0),
			}
			sessionCache := newCrossPolicySessionCache(winnerRole)
			concurrencyCache := &prioritySaturationConcurrencyCache{}
			concurrencyService := NewConcurrencyService(concurrencyCache)
			newService := func() *OpenAIGatewayService {
				cfg := &config.Config{}
				cfg.Gateway.OpenAIWS.LBTopK = 2
				cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
				cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Load = 1
				return &OpenAIGatewayService{
					accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
					cache:              sessionCache,
					cfg:                cfg,
					concurrencyService: concurrencyService,
				}
			}

			weighted := newDefaultOpenAIAccountScheduler(newService(), nil)
			priority := newPrioritySaturationOpenAIAccountScheduler(newService(), nil)
			request := OpenAIAccountScheduleRequest{
				Platform:               PlatformOpenAI,
				SessionHash:            "cross-policy-owner",
				CanTemporarilyOverflow: true,
				RequiredTransport:      OpenAIUpstreamTransportHTTPSSE,
			}

			type schedulerResult struct {
				role      string
				selection *AccountSelectionResult
				err       error
			}
			results := make(chan schedulerResult, 2)
			run := func(role string, scheduler OpenAIAccountScheduler) {
				ctx, cancel := context.WithTimeout(
					context.WithValue(context.Background(), crossPolicySessionRoleKey{}, role),
					5*time.Second,
				)
				defer cancel()
				selection, _, err := scheduler.Select(ctx, request)
				results <- schedulerResult{role: role, selection: selection, err: err}
			}
			go run("weighted", weighted)
			go run("priority", priority)

			got := []schedulerResult{<-results, <-results}
			for _, item := range got {
				require.NoError(t, item.err, item.role)
				require.NotNil(t, item.selection, item.role)
				require.NotNil(t, item.selection.Account, item.role)
			}

			var acquired *AccountSelectionResult
			var waiting *AccountSelectionResult
			for _, item := range got {
				if item.selection.Acquired {
					require.Nil(t, acquired)
					acquired = item.selection
				} else {
					require.Nil(t, waiting)
					waiting = item.selection
				}
			}
			require.NotNil(t, acquired)
			require.NotNil(t, waiting)
			require.NotNil(t, waiting.WaitPlan)
			require.Equal(t, acquired.Account.ID, waiting.Account.ID)
			require.Equal(t, acquired.Account.ID, waiting.SessionOwnerID)
			require.Equal(t, acquired.Account.ID, waiting.WaitPlan.AccountID)

			sessionCache.mu.Lock()
			require.Equal(t, acquired.Account.ID, sessionCache.sessionBindings["openai:cross-policy-owner"])
			sessionCache.mu.Unlock()

			concurrencyCache.mu.Lock()
			require.Equal(t, 1, concurrencyCache.active[acquired.Account.ID])
			for _, account := range accounts {
				if account.ID != acquired.Account.ID {
					require.Zero(t, concurrencyCache.active[account.ID])
				}
			}
			concurrencyCache.mu.Unlock()

			acquired.ReleaseFunc()
		})
	}
}
