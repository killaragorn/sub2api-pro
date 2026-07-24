package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

var prioritySaturationUnlimitedLeadingWarnings sync.Map

// prioritySaturationOpenAIAccountScheduler deterministically fills lower
// numeric priorities before moving to the next account. New and overflow
// requests use GeneralConcurrencyLimit; established affinity uses the full
// account concurrency so the configured reserve cannot be consumed by
// unrelated sessions.
type prioritySaturationOpenAIAccountScheduler struct {
	base *defaultOpenAIAccountScheduler
}

func newPrioritySaturationOpenAIAccountScheduler(service *OpenAIGatewayService, stats *openAIAccountRuntimeStats) OpenAIAccountScheduler {
	if stats == nil {
		stats = newOpenAIAccountRuntimeStats()
	}
	return &prioritySaturationOpenAIAccountScheduler{
		base: &defaultOpenAIAccountScheduler{service: service, stats: stats},
	}
}

func (s *prioritySaturationOpenAIAccountScheduler) Select(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (selection *AccountSelectionResult, decision OpenAIAccountScheduleDecision, err error) {
	startedAt := time.Now()
	defer func() {
		decision.LatencyMs = time.Since(startedAt).Milliseconds()
		s.base.metrics.recordSelect(decision)
	}()

	route, err := s.base.service.routeOpenAIAffinity(ctx, req, nil)
	applyOpenAIAffinityRouteDecision(&decision, route)
	if err != nil {
		return nil, decision, err
	}
	if route.Selection != nil {
		return route.Selection, decision, nil
	}
	req = route.Request
	overflowAffinityAccount := route.OverflowAccount
	canWaitForOverflow := canWaitForOpenAIAffinityOverflow(req, overflowAffinityAccount)

	selection, candidateCount, generalRejectCount, err := s.selectGeneralAccount(ctx, req)
	decision.Layer = openAIAccountScheduleLayerLoadBalance
	decision.CandidateCount = candidateCount
	decision.GeneralRejectCount = generalRejectCount
	if err != nil {
		if canWaitForOverflow &&
			(errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts)) {
			selection = s.base.service.openAIAffinityWaitSelection(req, overflowAffinityAccount)
			decision.AffinityWait = true
			setOpenAIAccountScheduleDecisionAccount(&decision, selection.Account)
			return selection, decision, nil
		}
		return nil, decision, err
	}
	if selection != nil && selection.WaitPlan != nil && canWaitForOverflow {
		selection = s.base.service.openAIAffinityWaitSelection(req, overflowAffinityAccount)
		decision.AffinityWait = true
	}
	if selection != nil {
		setOpenAIAccountScheduleDecisionAccount(&decision, selection.Account)
		decision.TemporaryOverflow = overflowAffinityAccount != nil &&
			selection.Acquired &&
			selection.Account != nil &&
			selection.Account.ID != overflowAffinityAccount.ID
	}
	return selection, decision, nil
}

func (s *prioritySaturationOpenAIAccountScheduler) selectGeneralAccount(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, int, int, error) {
	accounts, filterStats, err := s.eligibleAccounts(ctx, req)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(accounts) == 0 {
		return nil, 0, 0, noAvailableOpenAISelectionError(req.RequestedModel, false, filterStats.summary("priority_pool_empty"))
	}

	waitableAccountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if req.PreserveStickyBinding && account.ID == req.StickyAccountID {
			continue
		}
		selection, waitable, acquired, acquireErr := s.acquireWithFreshLimit(ctx, req, account, false)
		if acquireErr != nil {
			return nil, len(accounts), len(waitableAccountIDs), acquireErr
		}
		if !acquired || selection == nil || selection.Account == nil {
			if waitable != nil {
				waitableAccountIDs = append(waitableAccountIDs, waitable.ID)
			}
			continue
		}

		selection.PreserveStickyBinding = req.PreserveStickyBinding
		selection, settleErr := s.base.service.settleAcquiredOpenAISelection(ctx, req, selection)
		if settleErr != nil {
			return nil, len(accounts), len(waitableAccountIDs), settleErr
		}
		return selection, len(accounts), len(waitableAccountIDs), nil
	}

	for _, accountID := range waitableAccountIDs {
		fresh := s.freshEligibleAccount(ctx, req, accountID)
		if fresh == nil {
			continue
		}
		return attachOpenAISelectionRequest(
			s.waitPlan(fresh, fresh.ConcurrencyLimitForAffinity(false), false),
			req,
		), len(accounts), len(waitableAccountIDs), nil
	}
	return nil, len(accounts), len(waitableAccountIDs), noAvailableOpenAISelectionError(req.RequestedModel, false, filterStats.summary("priority_wait_candidate_stale"))
}

func (s *prioritySaturationOpenAIAccountScheduler) eligibleAccounts(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) ([]*Account, openAISelectionFilterStats, error) {
	accounts, err := s.base.service.listSchedulableAccounts(ctx, req.GroupID, req.Platform)
	stats := openAISelectionFilterStats{pool: len(accounts)}
	if err != nil {
		return nil, stats, err
	}

	var schedGroup *Group
	if req.GroupID != nil && s.base.service.schedulerSnapshot != nil {
		schedGroup, _ = s.base.service.schedulerSnapshot.GetGroupByID(ctx, *req.GroupID)
	}

	eligible := make([]*Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if _, excluded := req.ExcludedIDs[account.ID]; excluded {
			stats.exclude("excluded")
			continue
		}
		if !account.IsSchedulable() || account.Platform != normalizeOpenAICompatiblePlatform(req.Platform) || !account.IsOpenAICompatible() {
			stats.exclude("not_schedulable")
			continue
		}
		if schedGroup != nil && schedGroup.RequirePrivacySet && !account.IsPrivacySet() {
			stats.exclude("privacy_not_set")
			continue
		}
		if compatible, reason := s.base.isAccountRequestCompatibleReason(ctx, account, req); !compatible {
			stats.exclude(reason)
			continue
		}
		if !s.base.isAccountTransportCompatible(account, req.RequiredTransport) {
			stats.exclude("transport_incompatible")
			continue
		}
		if req.RequireCompact && openAICompactSupportTier(account) == 0 {
			stats.exclude("compact_unsupported")
			continue
		}
		eligible = append(eligible, account)
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].Priority != eligible[j].Priority {
			return eligible[i].Priority < eligible[j].Priority
		}
		return eligible[i].ID < eligible[j].ID
	})
	if len(eligible) > 0 && eligible[0].Concurrency <= 0 {
		warnPrioritySaturationUnlimitedLeadingAccount(eligible[0])
	}
	return eligible, stats, nil
}

func warnPrioritySaturationUnlimitedLeadingAccount(account *Account) {
	if account == nil {
		return
	}
	signature := fmt.Sprintf("%d:%d", account.Priority, account.Concurrency)
	if !storeAccountWarningSignature(&prioritySaturationUnlimitedLeadingWarnings, account.ID, signature) {
		return
	}
	slog.Warn(
		"OpenAI priority saturation leading account has unlimited concurrency and will absorb general traffic",
		"account_id", account.ID,
		"priority", account.Priority,
		"concurrency", account.Concurrency,
	)
}

func (s *prioritySaturationOpenAIAccountScheduler) freshEligibleAccount(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accountID int64,
) *Account {
	return s.base.freshEligibleAccount(ctx, req, accountID)
}

func (s *prioritySaturationOpenAIAccountScheduler) acquireWithFreshLimit(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	account *Account,
	affinity bool,
) (*AccountSelectionResult, *Account, bool, error) {
	limit := account.ConcurrencyLimitForAffinity(affinity)
	result, err := s.base.service.tryAcquireAccountSlot(ctx, account.ID, limit)
	if err != nil || result == nil || !result.Acquired {
		return nil, account, false, err
	}

	fresh := s.freshEligibleAccount(ctx, req, account.ID)
	if fresh == nil {
		result.ReleaseFunc()
		return nil, nil, false, nil
	}
	freshLimit := fresh.ConcurrencyLimitForAffinity(affinity)
	if freshLimit != limit {
		result.ReleaseFunc()
		result, err = s.base.service.tryAcquireAccountSlot(ctx, fresh.ID, freshLimit)
		if err != nil || result == nil || !result.Acquired {
			return nil, fresh, false, err
		}
	}
	return &AccountSelectionResult{
		Account:     fresh,
		Acquired:    true,
		ReleaseFunc: result.ReleaseFunc,
	}, nil, true, nil
}

func (s *prioritySaturationOpenAIAccountScheduler) waitPlan(account *Account, limit int, affinity bool) *AccountSelectionResult {
	if account == nil {
		return nil
	}
	cfg := s.base.service.schedulingConfig()
	timeout := cfg.FallbackWaitTimeout
	maxWaiting := cfg.FallbackMaxWaiting
	if affinity {
		timeout = cfg.StickySessionWaitTimeout
		maxWaiting = cfg.StickySessionMaxWaiting
	}
	return &AccountSelectionResult{
		Account: account,
		WaitPlan: &AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: limit,
			Timeout:        timeout,
			MaxWaiting:     maxWaiting,
		},
	}
}

func setOpenAIAccountScheduleDecisionAccount(decision *OpenAIAccountScheduleDecision, account *Account) {
	if decision == nil || account == nil {
		return
	}
	decision.SelectedAccountID = account.ID
	decision.SelectedAccountType = account.Type
	decision.GeneralLimit = account.GeneralConcurrencyLimit()
	decision.HardLimit = account.Concurrency
	decision.AffinityReserve = account.GetAffinityConcurrencyReserve()
}

func (s *prioritySaturationOpenAIAccountScheduler) ReportResult(accountID int64, success bool, firstTokenMs *int) {
	s.base.ReportResult(accountID, success, firstTokenMs)
}

func (s *prioritySaturationOpenAIAccountScheduler) ReportSwitch() {
	s.base.ReportSwitch()
}

func (s *prioritySaturationOpenAIAccountScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	return s.base.SnapshotMetrics()
}
