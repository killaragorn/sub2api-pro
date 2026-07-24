package service

import (
	"context"
	"fmt"
	"strings"
)

const openAIStickyOwnerReconcileAttempts = 8

func attachOpenAISelectionRequest(selection *AccountSelectionResult, req OpenAIAccountScheduleRequest) *AccountSelectionResult {
	if selection == nil {
		return nil
	}
	request := req
	selection.openAIRequest = &request
	if selection.SessionOwnerID <= 0 {
		selection.SessionOwnerID = req.StickyAccountID
	}
	selection.PreserveStickyBinding = selection.PreserveStickyBinding || req.PreserveStickyBinding
	return selection
}
func selectionSessionOwnerID(selection *AccountSelectionResult) int64 {
	if selection == nil {
		return 0
	}
	return selection.SessionOwnerID
}

type openAIAffinityRouteResult struct {
	Request            OpenAIAccountScheduleRequest
	Selection          *AccountSelectionResult
	OverflowAccount    *Account
	Layer              string
	AffinityReserveHit bool
	Rejected           bool
}

func canWaitForOpenAIAffinityOverflow(
	req OpenAIAccountScheduleRequest,
	account *Account,
) bool {
	if account == nil {
		return false
	}
	_, excluded := req.ExcludedIDs[account.ID]
	return !excluded
}

// routeOpenAIAffinity applies protocol-level migration rules before a
// scheduler considers general candidates. All scheduler policies use this
// state machine so changing the policy cannot change whether upstream state is
// allowed to move between accounts.
func (s *OpenAIGatewayService) routeOpenAIAffinity(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	shouldEscapeSession func(accountID int64) bool,
) (openAIAffinityRouteResult, error) {
	route := openAIAffinityRouteResult{Request: req}

	previousResponseID := strings.TrimSpace(req.PreviousResponseID)
	if previousResponseID != "" && normalizeOpenAICompatiblePlatform(req.Platform) == PlatformOpenAI {
		accountID := s.ResolveAccountIDByPreviousResponseIDForScheduler(
			ctx,
			req.GroupID,
			previousResponseID,
			req.RequestedModel,
			nil,
			req.RequiredCapability,
			req.RequireCompact,
		)
		canOverflow := req.PreviousResponseCanMove && req.CanTemporarilyOverflow
		if accountID > 0 {
			selection, account, reserveHit, err := s.selectOpenAIAffinityAccount(
				ctx,
				route.Request,
				accountID,
				!canOverflow,
				true,
			)
			if err != nil {
				return route, err
			}
			if selection != nil {
				route.Selection = selection
				route.Layer = openAIAccountScheduleLayerPreviousResponse
				route.AffinityReserveHit = reserveHit
				return route, nil
			}
			if !canOverflow {
				route.Rejected = true
				return route, noAvailableOpenAISelectionError(req.RequestedModel, false, "previous_response_affinity_unavailable")
			}
			if account != nil {
				if err := s.ConvergeStickySession(ctx, req.GroupID, req.SessionHash, accountID); err != nil {
					return route, err
				}
				route.Request.StickyAccountID = accountID
				route.Request.PreserveStickyBinding = true
				route.OverflowAccount = account
				return route, nil
			}
		} else if !canOverflow {
			route.Rejected = true
			return route, noAvailableOpenAISelectionError(req.RequestedModel, false, "previous_response_affinity_missing")
		}
	}

	ownerID := route.Request.StickyAccountID
	if ownerID <= 0 {
		return route, nil
	}

	if req.CanTemporarilyOverflow && shouldEscapeSession != nil {
		base := &defaultOpenAIAccountScheduler{service: s}
		account := base.freshEligibleAccount(ctx, route.Request, ownerID)
		if account != nil {
			if _, excluded := route.Request.ExcludedIDs[ownerID]; !excluded && shouldEscapeSession(ownerID) {
				route.Request.PreserveStickyBinding = true
				route.OverflowAccount = account
				return route, nil
			}
		}
	}

	selection, account, reserveHit, err := s.selectOpenAIAffinityAccount(
		ctx,
		route.Request,
		ownerID,
		!req.CanTemporarilyOverflow,
		false,
	)
	if err != nil {
		return route, err
	}
	if selection != nil {
		route.Selection = selection
		route.Layer = openAIAccountScheduleLayerSessionSticky
		route.AffinityReserveHit = reserveHit
		return route, nil
	}
	if account != nil && req.CanTemporarilyOverflow {
		route.Request.PreserveStickyBinding = true
		route.OverflowAccount = account
		return route, nil
	}
	if !req.CanTemporarilyOverflow {
		route.Rejected = true
		return route, noAvailableOpenAISelectionError(req.RequestedModel, false, "session_affinity_unavailable")
	}
	if _, excluded := route.Request.ExcludedIDs[ownerID]; !excluded {
		// The old owner is no longer eligible. Keep the Redis binding until a
		// replacement has actually acquired a slot, then let settlement migrate
		// it atomically. Clearing StickyAccountID prevents weighted fallback from
		// treating this as an ordinary temporary overflow.
		route.Request.StickyAccountID = 0
		route.Request.PreserveStickyBinding = false
	}
	return route, nil
}

func (s *OpenAIGatewayService) selectOpenAIAffinityAccount(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accountID int64,
	waitWhenFull bool,
	convergeStickyBinding bool,
) (*AccountSelectionResult, *Account, bool, error) {
	base := &defaultOpenAIAccountScheduler{service: s}
	account := base.freshEligibleAccount(ctx, req, accountID)
	if account == nil {
		return nil, nil, false, nil
	}
	if _, excluded := req.ExcludedIDs[accountID]; excluded {
		// A request-scoped exclusion represents retry failover, not permanent
		// owner invalidation. Return the owner so general selection preserves
		// the binding and reports this attempt as a temporary overflow.
		return nil, account, false, nil
	}

	limit := account.ConcurrencyLimitForAffinity(true)
	result, err := s.tryAcquireAccountSlot(ctx, accountID, limit)
	if err != nil {
		return nil, account, false, err
	}
	if result != nil && result.Acquired {
		selection := &AccountSelectionResult{
			Account:        account,
			Acquired:       true,
			ReleaseFunc:    result.ReleaseFunc,
			SessionOwnerID: req.StickyAccountID,
		}
		if convergeStickyBinding {
			markOpenAISelectionForStickyConvergence(selection)
		}
		selection, err = s.settleAcquiredOpenAISelection(ctx, req, selection)
		if err != nil {
			return nil, account, false, err
		}
		return selection, account, s.openAIAffinityReserveInUse(ctx, account), nil
	}
	if !waitWhenFull {
		return nil, account, false, nil
	}

	cfg := s.schedulingConfig()
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:        account,
		SessionOwnerID: req.StickyAccountID,
		WaitPlan: &AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: limit,
			Timeout:        cfg.StickySessionWaitTimeout,
			MaxWaiting:     cfg.StickySessionMaxWaiting,
		},
	}, req)
	if convergeStickyBinding {
		markOpenAISelectionForStickyConvergence(selection)
	}
	return selection, account, false, nil
}

func (s *OpenAIGatewayService) openAIAffinityReserveInUse(ctx context.Context, account *Account) bool {
	if s == nil || s.concurrencyService == nil || account == nil || account.GetAffinityConcurrencyReserve() <= 0 {
		return false
	}
	counts, err := s.concurrencyService.GetAccountConcurrencyBatch(ctx, []int64{account.ID})
	return err == nil && counts[account.ID] > account.GeneralConcurrencyLimit()
}

func (s *OpenAIGatewayService) openAIAffinityWaitSelection(
	req OpenAIAccountScheduleRequest,
	account *Account,
) *AccountSelectionResult {
	if account == nil {
		return nil
	}
	cfg := s.schedulingConfig()
	return attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:               account,
		SessionOwnerID:        account.ID,
		PreserveStickyBinding: req.PreserveStickyBinding,
		WaitPlan: &AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: account.ConcurrencyLimitForAffinity(true),
			Timeout:        cfg.StickySessionWaitTimeout,
			MaxWaiting:     cfg.StickySessionMaxWaiting,
		},
	}, req)
}

func (s *OpenAIGatewayService) finalizeLegacyOpenAISelection(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	selection *AccountSelectionResult,
) (*AccountSelectionResult, error) {
	if selection == nil {
		return nil, nil
	}
	if req.StickyAccountID <= 0 {
		req.StickyAccountID = selection.SessionOwnerID
	}
	selection = attachOpenAISelectionRequest(selection, req)
	if !selection.Acquired {
		return selection, nil
	}
	return s.settleAcquiredOpenAISelection(ctx, req, selection)
}

func markOpenAISelectionForStickyConvergence(selection *AccountSelectionResult) *AccountSelectionResult {
	if selection != nil {
		selection.convergeStickyBinding = true
	}
	return selection
}

// ConvergeStickySession atomically makes accountID the canonical owner. This is
// used only for stronger previous_response_id affinity or an unusable old owner;
// ordinary retries and overflow never call it.
func (s *OpenAIGatewayService) ConvergeStickySession(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	accountID int64,
) error {
	_, _, _, _, err := s.convergeStickySession(ctx, groupID, sessionHash, accountID, false)
	return err
}

func (s *OpenAIGatewayService) convergeStickySession(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	accountID int64,
	guardRollback bool,
) (claimed bool, previousOwnerID int64, rollbackToken string, legacyClaimed bool, err error) {
	sessionHash = strings.TrimSpace(sessionHash)
	if s == nil || sessionHash == "" || accountID <= 0 {
		return false, 0, "", false, nil
	}
	for range openAIStickyOwnerReconcileAttempts {
		var ownerID int64
		var claimToken string
		var claimLegacy bool
		if guardRollback {
			ownerID, claimed, claimToken, claimLegacy, err = s.claimStickySessionForSelection(
				ctx,
				groupID,
				sessionHash,
				accountID,
			)
		} else {
			ownerID, claimed, err = s.ClaimStickySession(ctx, groupID, sessionHash, accountID)
		}
		if err != nil {
			return false, 0, "", false, err
		}
		if ownerID == accountID {
			return claimed, 0, claimToken, claimLegacy, nil
		}
		var swapToken string
		var swapped bool
		if guardRollback {
			swapped, swapToken, err = s.migrateStickySessionForSelection(
				ctx,
				groupID,
				sessionHash,
				ownerID,
				accountID,
			)
		} else {
			swapped, err = s.MigrateStickySession(ctx, groupID, sessionHash, ownerID, accountID)
		}
		if err != nil {
			return false, 0, "", false, err
		}
		if swapped {
			return false, ownerID, swapToken, false, nil
		}
	}
	return false, 0, "", false, fmt.Errorf("sticky session owner changed too frequently")
}

// FinalizeAcquiredOpenAISelection is called after a handler has acquired a
// WaitPlan. It closes the only interval in which two new requests can race to
// claim one session: the loser releases its provisional slot and follows the
// canonical owner (or receives that owner's wait plan).
func (s *OpenAIGatewayService) FinalizeAcquiredOpenAISelection(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	selection *AccountSelectionResult,
) (*AccountSelectionResult, error) {
	if selection == nil || !selection.Acquired || selection.Account == nil {
		return selection, nil
	}
	req := OpenAIAccountScheduleRequest{
		GroupID:     groupID,
		Platform:    selection.Account.Platform,
		SessionHash: sessionHash,
	}
	if selection.openAIRequest != nil {
		req = *selection.openAIRequest
		req.GroupID = groupID
		if strings.TrimSpace(sessionHash) != "" {
			req.SessionHash = sessionHash
		}
	}
	if selection.WaitPlan != nil {
		staleAccountID := selection.Account.ID
		var err error
		var becameIneligible bool
		selection, becameIneligible, err = s.revalidateAcquiredOpenAIWaitSelection(ctx, req, selection)
		if becameIneligible {
			return s.rescheduleOpenAISelectionAfterStaleWait(ctx, req, staleAccountID)
		}
		if err != nil || selection == nil || !selection.Acquired {
			return selection, err
		}
	}
	return s.settleAcquiredOpenAISelection(ctx, req, selection)
}

func (s *OpenAIGatewayService) rescheduleOpenAISelectionAfterStaleWait(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	staleAccountID int64,
) (*AccountSelectionResult, error) {
	excludedIDs := cloneExcludedAccountIDs(req.ExcludedIDs)
	if excludedIDs == nil {
		excludedIDs = make(map[int64]struct{})
	}
	// A stale general/overflow target must not be selected again from an old
	// snapshot. A stale session owner is different: leaving it unexcluded lets
	// routeOpenAIAffinity recognize permanent ineligibility and CAS-migrate the
	// canonical owner instead of treating this as a request-scoped failover.
	if staleAccountID > 0 && staleAccountID != req.StickyAccountID {
		excludedIDs[staleAccountID] = struct{}{}
	}

	selection, _, err := s.selectAccountWithScheduler(
		ctx,
		req.GroupID,
		req.PreviousResponseID,
		req.SessionHash,
		req.RequestedModel,
		excludedIDs,
		req.RequiredTransport,
		req.RequiredCapability,
		req.RequiredImageCapability,
		req.RequireCompact,
		req.Platform,
		req.PreviousResponseCanMove,
		req.CanTemporarilyOverflow,
		req.UseUpstreamTokenCost,
	)
	return selection, err
}

// revalidateAcquiredOpenAIWaitSelection closes the stale-plan window between
// selecting a wait target and actually acquiring its slot. It also reacquires
// when an administrator changed C/R/G while the request was waiting.
func (s *OpenAIGatewayService) revalidateAcquiredOpenAIWaitSelection(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	selection *AccountSelectionResult,
) (*AccountSelectionResult, bool, error) {
	if s == nil || selection == nil || !selection.Acquired || selection.Account == nil || selection.WaitPlan == nil {
		return selection, false, nil
	}

	plan := *selection.WaitPlan
	fresh := selection.Account
	if s.schedulerSnapshot != nil || s.accountRepo != nil {
		fresh = (&defaultOpenAIAccountScheduler{service: s}).freshEligibleAccount(ctx, req, selection.Account.ID)
		if fresh == nil {
			releaseOpenAISelection(selection)
			return nil, true, nil
		}
	}

	ownerID := selection.SessionOwnerID
	if ownerID <= 0 {
		ownerID = req.StickyAccountID
	}
	affinity := ownerID > 0 && ownerID == fresh.ID
	freshLimit := fresh.ConcurrencyLimitForAffinity(affinity)
	selection.Account = fresh

	if plan.MaxConcurrency == freshLimit {
		selection.WaitPlan = nil
		return selection, false, nil
	}

	releaseOpenAISelection(selection)
	result, err := s.tryAcquireAccountSlot(ctx, fresh.ID, freshLimit)
	if err != nil {
		return nil, false, err
	}
	if result != nil && result.Acquired {
		selection.Acquired = true
		selection.ReleaseFunc = result.ReleaseFunc
		selection.WaitPlan = nil
		return selection, false, nil
	}

	selection.WaitPlan = &AccountWaitPlan{
		AccountID:      fresh.ID,
		MaxConcurrency: freshLimit,
		Timeout:        plan.Timeout,
		MaxWaiting:     plan.MaxWaiting,
	}
	return selection, false, nil
}

// FinalizeOpenAIResponseAffinity converges a temporary overflow only after the
// generated Responses state has been durably associated with the selected
// account. The CAS is intentionally limited to the owner observed during
// selection so a concurrent migration is never overwritten.
func (s *OpenAIGatewayService) FinalizeOpenAIResponseAffinity(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	selection *AccountSelectionResult,
	result *OpenAIForwardResult,
) (bool, error) {
	if s == nil || selection == nil || selection.Account == nil || result == nil ||
		!selection.PreserveStickyBinding || !result.ResponseAccountBound ||
		!result.SucceededForScheduling() ||
		selection.Account.Platform != PlatformOpenAI {
		return false, nil
	}
	sessionHash = strings.TrimSpace(sessionHash)
	oldOwnerID := selection.SessionOwnerID
	newOwnerID := selection.Account.ID
	if sessionHash == "" || oldOwnerID <= 0 || newOwnerID <= 0 || oldOwnerID == newOwnerID {
		return false, nil
	}

	responseID := strings.TrimSpace(result.ResponseID)
	if ClassifyOpenAIPreviousResponseIDKind(responseID) != OpenAIPreviousResponseIDKindResponseID {
		responseID = strings.TrimSpace(result.RequestID)
	}
	if ClassifyOpenAIPreviousResponseIDKind(responseID) != OpenAIPreviousResponseIDKindResponseID {
		return false, nil
	}
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return false, nil
	}
	mappedAccountID, err := store.GetResponseAccount(ctx, derefGroupID(groupID), responseID)
	if err != nil {
		return false, err
	}
	if mappedAccountID != newOwnerID {
		return false, fmt.Errorf(
			"response affinity mapping mismatch: response=%s selected_account=%d mapped_account=%d",
			responseID,
			newOwnerID,
			mappedAccountID,
		)
	}

	swapped, err := s.MigrateStickySession(ctx, groupID, sessionHash, oldOwnerID, newOwnerID)
	if err != nil || !swapped {
		return swapped, err
	}
	selection.SessionOwnerID = newOwnerID
	selection.PreserveStickyBinding = false
	return true, nil
}

func (s *OpenAIGatewayService) settleAcquiredOpenAISelection(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	selection *AccountSelectionResult,
) (*AccountSelectionResult, error) {
	selection = attachOpenAISelectionRequest(selection, req)
	if selection == nil || !selection.Acquired || selection.Account == nil {
		return selection, nil
	}
	sessionHash := strings.TrimSpace(req.SessionHash)
	if sessionHash == "" {
		return selection, nil
	}

	selectedID := selection.Account.ID
	if selection.convergeStickyBinding {
		claimed, previousOwnerID, rollbackToken, legacyClaimed, err := s.convergeStickySession(
			ctx,
			req.GroupID,
			sessionHash,
			selectedID,
			true,
		)
		if err != nil {
			releaseOpenAISelection(selection)
			return nil, err
		}
		selection.stickyBindingClaimed = selection.stickyBindingClaimed || claimed
		if previousOwnerID > 0 {
			selection.stickyBindingPreviousOwnerID = previousOwnerID
		}
		if rollbackToken != "" {
			selection.stickyBindingRollbackToken = rollbackToken
		}
		selection.stickyBindingLegacyClaimed = selection.stickyBindingLegacyClaimed || legacyClaimed
		selection.SessionOwnerID = selectedID
		selection.PreserveStickyBinding = false
		return selection, nil
	}

	knownOwnerID := selection.SessionOwnerID
	if knownOwnerID <= 0 {
		knownOwnerID = req.StickyAccountID
	}
	preserve := selection.PreserveStickyBinding || req.PreserveStickyBinding
	if preserve {
		selection.SessionOwnerID = knownOwnerID
		selection.PreserveStickyBinding = true
		if knownOwnerID > 0 {
			_ = s.refreshStickySessionTTL(ctx, req.GroupID, sessionHash, knownOwnerID, s.openAIWSSessionStickyTTL())
		}
		return selection, nil
	}

	ownerID, claimed, rollbackToken, legacyClaimed, err := s.claimStickySessionForSelection(
		ctx,
		req.GroupID,
		sessionHash,
		selectedID,
	)
	if err != nil {
		releaseOpenAISelection(selection)
		return nil, err
	}
	if ownerID <= 0 || ownerID == selectedID {
		selection.stickyBindingClaimed = selection.stickyBindingClaimed || claimed
		if rollbackToken != "" {
			selection.stickyBindingRollbackToken = rollbackToken
		}
		selection.stickyBindingLegacyClaimed = selection.stickyBindingLegacyClaimed || legacyClaimed
		selection.SessionOwnerID = selectedID
		return selection, nil
	}
	return s.followCanonicalStickyOwner(ctx, req, selection, ownerID)
}

func (s *OpenAIGatewayService) followCanonicalStickyOwner(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	provisional *AccountSelectionResult,
	ownerID int64,
) (*AccountSelectionResult, error) {
	selectedID := provisional.Account.ID
	base := &defaultOpenAIAccountScheduler{service: s}

	for range openAIStickyOwnerReconcileAttempts {
		if ownerID <= 0 || ownerID == selectedID {
			provisional.SessionOwnerID = selectedID
			return attachOpenAISelectionRequest(provisional, req), nil
		}
		if _, excluded := req.ExcludedIDs[ownerID]; excluded {
			if !req.CanTemporarilyOverflow {
				releaseOpenAISelection(provisional)
				return nil, noAvailableOpenAISelectionError(
					req.RequestedModel,
					false,
					"canonical_session_owner_excluded",
				)
			}
			provisional.SessionOwnerID = ownerID
			provisional.PreserveStickyBinding = true
			_ = s.refreshStickySessionTTL(ctx, req.GroupID, req.SessionHash, ownerID, s.openAIWSSessionStickyTTL())
			return attachOpenAISelectionRequest(provisional, req), nil
		}

		ownerReq := req
		ownerReq.ExcludedIDs = nil
		ownerReq.StickyAccountID = ownerID
		ownerReq.PreserveStickyBinding = false
		owner := base.freshEligibleAccount(ctx, ownerReq, ownerID)
		if owner == nil {
			swapped, rollbackToken, err := s.migrateStickySessionForSelection(
				ctx,
				req.GroupID,
				req.SessionHash,
				ownerID,
				selectedID,
			)
			if err != nil {
				releaseOpenAISelection(provisional)
				return nil, err
			}
			if swapped {
				provisional.stickyBindingPreviousOwnerID = ownerID
				provisional.stickyBindingRollbackToken = rollbackToken
				provisional.SessionOwnerID = selectedID
				return attachOpenAISelectionRequest(provisional, req), nil
			}
			var claimed bool
			var claimToken string
			var legacyClaimed bool
			ownerID, claimed, claimToken, legacyClaimed, err = s.claimStickySessionForSelection(
				ctx,
				req.GroupID,
				req.SessionHash,
				selectedID,
			)
			if err != nil {
				releaseOpenAISelection(provisional)
				return nil, err
			}
			provisional.stickyBindingClaimed = provisional.stickyBindingClaimed || claimed
			if claimToken != "" {
				provisional.stickyBindingRollbackToken = claimToken
			}
			provisional.stickyBindingLegacyClaimed = legacyClaimed
			continue
		}

		releaseOpenAISelection(provisional)
		limit := owner.ConcurrencyLimitForAffinity(true)
		result, err := s.tryAcquireAccountSlot(ctx, owner.ID, limit)
		if err != nil {
			return nil, err
		}
		if result == nil || !result.Acquired {
			cfg := s.schedulingConfig()
			return attachOpenAISelectionRequest(&AccountSelectionResult{
				Account:        owner,
				SessionOwnerID: owner.ID,
				WaitPlan: &AccountWaitPlan{
					AccountID:      owner.ID,
					MaxConcurrency: limit,
					Timeout:        cfg.StickySessionWaitTimeout,
					MaxWaiting:     cfg.StickySessionMaxWaiting,
				},
			}, ownerReq), nil
		}

		actualOwnerID, claimed, rollbackToken, legacyClaimed, claimErr := s.claimStickySessionForSelection(
			ctx,
			req.GroupID,
			req.SessionHash,
			owner.ID,
		)
		if claimErr != nil {
			result.ReleaseFunc()
			return nil, claimErr
		}
		if actualOwnerID == owner.ID {
			return attachOpenAISelectionRequest(&AccountSelectionResult{
				Account:                    owner,
				Acquired:                   true,
				ReleaseFunc:                result.ReleaseFunc,
				SessionOwnerID:             owner.ID,
				stickyBindingClaimed:       claimed,
				stickyBindingRollbackToken: rollbackToken,
				stickyBindingLegacyClaimed: legacyClaimed,
			}, ownerReq), nil
		}
		provisional = &AccountSelectionResult{
			Account:     owner,
			Acquired:    true,
			ReleaseFunc: result.ReleaseFunc,
		}
		selectedID = owner.ID
		ownerID = actualOwnerID
	}
	releaseOpenAISelection(provisional)
	return nil, fmt.Errorf("sticky session owner changed too frequently")
}

func releaseOpenAISelection(selection *AccountSelectionResult) {
	if selection != nil && selection.Acquired && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
		selection.Acquired = false
		selection.ReleaseFunc = nil
	}
}

// RejectAcquiredOpenAISelection rolls back scheduler state when a handler
// rejects an account during post-selection validation. Only an acquired
// selection can have changed owner state: a fresh claim is deleted and a CAS
// migration is restored. Existing owners and unacquired wait plans are kept.
func (s *OpenAIGatewayService) RejectAcquiredOpenAISelection(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	selection *AccountSelectionResult,
) error {
	if selection == nil {
		return nil
	}
	accountID := int64(0)
	if selection.Account != nil {
		accountID = selection.Account.ID
	}
	wasAcquired := selection.Acquired
	claimed := selection.stickyBindingClaimed
	previousOwnerID := selection.stickyBindingPreviousOwnerID
	rollbackToken := selection.stickyBindingRollbackToken
	legacyClaimed := selection.stickyBindingLegacyClaimed
	selection.stickyBindingRollbackToken = ""
	selection.stickyBindingLegacyClaimed = false
	releaseOpenAISelection(selection)
	if !wasAcquired || accountID <= 0 || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	if rollbackToken != "" {
		_, err := s.rollbackStickySessionForSelection(
			ctx,
			groupID,
			sessionHash,
			accountID,
			previousOwnerID,
			rollbackToken,
			legacyClaimed,
		)
		return err
	}
	if _, supportsGuardedRollback := s.cache.(OpenAISessionOwnerRollbackCache); supportsGuardedRollback {
		return nil
	}
	if claimed {
		return s.deleteStickySessionAccountID(ctx, groupID, sessionHash, accountID)
	}
	if previousOwnerID > 0 && previousOwnerID != accountID {
		_, err := s.MigrateStickySession(ctx, groupID, sessionHash, accountID, previousOwnerID)
		return err
	}
	return nil
}
