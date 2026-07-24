package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIAffinityPolicyTestCase struct {
	name               string
	advanced           string
	stickyWeighted     string
	prioritySaturation string
}

func openAIAffinityPolicyTestCases() []openAIAffinityPolicyTestCase {
	return []openAIAffinityPolicyTestCase{
		{name: "legacy", advanced: "false"},
		{name: "default", advanced: "true"},
		{name: "weighted", advanced: "true", stickyWeighted: "true"},
		{name: "priority saturation", advanced: "false", prioritySaturation: "true"},
	}
}

func TestOpenAIAffinityRouting_FullSessionOverflowHonorsExplicitPermissionAcrossPolicies(t *testing.T) {
	for _, tc := range openAIAffinityPolicyTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(81)
			owner := prioritySaturationTestAccount(8101, 1, 1, 0)
			owner.GroupIDs = []int64{groupID}
			overflow := prioritySaturationTestAccount(8102, 2, 2, 0)
			overflow.GroupIDs = []int64{groupID}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{
				"openai:affinity-policy-session": owner.ID,
			}}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner, overflow}},
				cache:       cache,
				cfg:         &config.Config{},
				rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(
					tc.advanced,
					tc.stickyWeighted,
					"",
					tc.prioritySaturation,
				),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
					acquireResults: map[int64]bool{owner.ID: false, overflow.ID: true},
					loadMap: map[int64]*AccountLoadInfo{
						owner.ID:    {AccountID: owner.ID, CurrentConcurrency: 1, LoadRate: 100},
						overflow.ID: {AccountID: overflow.ID, LoadRate: 0},
					},
				}),
			}

			selection, decision, err := svc.SelectAccountWithSchedulerForCapabilityOptions(
				context.Background(),
				&groupID,
				"",
				"affinity-policy-session",
				"gpt-5.1",
				nil,
				OpenAIUpstreamTransportHTTPSSE,
				OpenAIEndpointCapabilityChatCompletions,
				false,
				OpenAIAccountSchedulingOptions{
					CanTemporarilyOverflow: true,
					Platform:               PlatformOpenAI,
				},
			)

			require.NoError(t, err)
			require.NotNil(t, selection)
			require.True(t, selection.Acquired)
			require.Equal(t, overflow.ID, selection.Account.ID)
			require.True(t, selection.PreserveStickyBinding)
			require.True(t, decision.TemporaryOverflow)
			require.Equal(t, owner.ID, cache.sessionBindings["openai:affinity-policy-session"])
			selection.ReleaseFunc()
		})
	}
}

func TestOpenAIAffinityRouting_ImmovableSessionWaitsAcrossPolicies(t *testing.T) {
	for _, tc := range openAIAffinityPolicyTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(82)
			owner := prioritySaturationTestAccount(8201, 1, 1, 0)
			owner.GroupIDs = []int64{groupID}
			other := prioritySaturationTestAccount(8202, 2, 1, 0)
			other.GroupIDs = []int64{groupID}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{
				"openai:immovable-policy-session": owner.ID,
			}}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner, other}},
				cache:       cache,
				cfg:         &config.Config{},
				rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(
					tc.advanced,
					tc.stickyWeighted,
					"",
					tc.prioritySaturation,
				),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
					acquireResults: map[int64]bool{owner.ID: false, other.ID: true},
				}),
			}

			selection, decision, err := svc.SelectAccountWithSchedulerForCapabilityOptions(
				context.Background(),
				&groupID,
				"",
				"immovable-policy-session",
				"gpt-5.1",
				nil,
				OpenAIUpstreamTransportHTTPSSE,
				OpenAIEndpointCapabilityChatCompletions,
				false,
				OpenAIAccountSchedulingOptions{Platform: PlatformOpenAI},
			)

			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.WaitPlan)
			require.Equal(t, owner.ID, selection.Account.ID)
			require.Equal(t, owner.ID, selection.WaitPlan.AccountID)
			require.True(t, decision.StickySessionHit)
			require.True(t, decision.AffinityWait)
		})
	}
}

func TestOpenAIAffinityRouting_MovablePreviousResponseOverflowsAcrossPolicies(t *testing.T) {
	for _, tc := range openAIAffinityPolicyTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(83)
			owner := prioritySaturationTestAccount(8301, 1, 1, 0)
			owner.GroupIDs = []int64{groupID}
			owner.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			overflow := prioritySaturationTestAccount(8302, 2, 2, 0)
			overflow.GroupIDs = []int64{groupID}
			overflow.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			cache := &schedulerTestGatewayCache{}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner, overflow}},
				cache:       cache,
				cfg:         newSchedulerTestOpenAIWSV2Config(),
				rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(
					tc.advanced,
					tc.stickyWeighted,
					"",
					tc.prioritySaturation,
				),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
					acquireResults: map[int64]bool{owner.ID: false, overflow.ID: true},
					loadMap: map[int64]*AccountLoadInfo{
						owner.ID:    {AccountID: owner.ID, CurrentConcurrency: 1, LoadRate: 100},
						overflow.ID: {AccountID: overflow.ID, LoadRate: 0},
					},
				}),
			}
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(
				context.Background(),
				groupID,
				"resp-affinity-policy",
				owner.ID,
				time.Hour,
			))

			selection, decision, err := svc.SelectAccountWithSchedulerForCapabilityOptions(
				context.Background(),
				&groupID,
				"resp-affinity-policy",
				"previous-affinity-policy-session",
				"gpt-5.1",
				nil,
				OpenAIUpstreamTransportHTTPSSE,
				OpenAIEndpointCapabilityChatCompletions,
				false,
				OpenAIAccountSchedulingOptions{
					PreviousResponseCanMove: true,
					CanTemporarilyOverflow:  true,
					Platform:                PlatformOpenAI,
				},
			)

			require.NoError(t, err)
			require.NotNil(t, selection)
			require.True(t, selection.Acquired)
			require.Equal(t, overflow.ID, selection.Account.ID)
			require.True(t, selection.PreserveStickyBinding)
			require.True(t, decision.TemporaryOverflow)
			require.Equal(t, owner.ID, cache.sessionBindings["openai:previous-affinity-policy-session"])
			selection.ReleaseFunc()
		})
	}
}

func TestOpenAIAffinityRouting_MissingImmovablePreviousResponseRejectsAcrossPolicies(t *testing.T) {
	for _, tc := range openAIAffinityPolicyTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(84)
			account := prioritySaturationTestAccount(8401, 1, 1, 0)
			account.GroupIDs = []int64{groupID}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
				cache:       &schedulerTestGatewayCache{},
				cfg:         newSchedulerTestOpenAIWSV2Config(),
				rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(
					tc.advanced,
					tc.stickyWeighted,
					"",
					tc.prioritySaturation,
				),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}

			selection, decision, err := svc.SelectAccountWithSchedulerForCapabilityOptions(
				context.Background(),
				&groupID,
				"missing-response",
				"",
				"gpt-5.1",
				nil,
				OpenAIUpstreamTransportHTTPSSE,
				OpenAIEndpointCapabilityChatCompletions,
				false,
				OpenAIAccountSchedulingOptions{Platform: PlatformOpenAI},
			)

			require.Nil(t, selection)
			require.ErrorContains(t, err, "previous_response_affinity_missing")
			require.True(t, decision.AffinityRejected)
		})
	}
}

func TestOpenAIAffinityRouting_ExcludedOwnerWithoutFallbackNeverWaitsAcrossPolicies(t *testing.T) {
	for _, tc := range openAIAffinityPolicyTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(85)
			owner := prioritySaturationTestAccount(8501, 1, 1, 0)
			owner.GroupIDs = []int64{groupID}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{
				"openai:excluded-owner-session": owner.ID,
			}}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner}},
				cache:       cache,
				cfg:         &config.Config{},
				rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(
					tc.advanced,
					tc.stickyWeighted,
					"",
					tc.prioritySaturation,
				),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{
					acquireResults: map[int64]bool{owner.ID: true},
				}),
			}

			selection, decision, err := svc.SelectAccountWithSchedulerForCapabilityOptions(
				context.Background(),
				&groupID,
				"",
				"excluded-owner-session",
				"gpt-5.1",
				map[int64]struct{}{owner.ID: {}},
				OpenAIUpstreamTransportHTTPSSE,
				OpenAIEndpointCapabilityChatCompletions,
				false,
				OpenAIAccountSchedulingOptions{
					CanTemporarilyOverflow: true,
					Platform:               PlatformOpenAI,
				},
			)

			require.Nil(t, selection)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.False(t, decision.AffinityWait)
			require.Equal(t, owner.ID, cache.sessionBindings["openai:excluded-owner-session"])
		})
	}
}

func TestOpenAIAffinityRouting_ImmovableClaimRaceRejectsExcludedOwnerAcrossPolicies(t *testing.T) {
	for _, tc := range openAIAffinityPolicyTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(86)
			owner := prioritySaturationTestAccount(8601, 1, 2, 0)
			owner.GroupIDs = []int64{groupID}
			provisional := prioritySaturationTestAccount(8602, 2, 2, 0)
			provisional.GroupIDs = []int64{groupID}
			sessionCache := &rotatingPrioritySaturationSessionCache{
				prioritySaturationSessionCache: &prioritySaturationSessionCache{
					schedulerTestGatewayCache: schedulerTestGatewayCache{
						sessionBindings: map[string]int64{},
					},
				},
				owners: []int64{owner.ID},
			}
			concurrencyCache := &prioritySaturationConcurrencyCache{}
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{owner, provisional}},
				cache:       sessionCache,
				cfg:         &config.Config{},
				rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(
					tc.advanced,
					tc.stickyWeighted,
					"",
					tc.prioritySaturation,
				),
				concurrencyService: NewConcurrencyService(concurrencyCache),
			}

			selection, _, err := svc.SelectAccountWithSchedulerForCapabilityOptions(
				context.Background(),
				&groupID,
				"",
				"claim-race-excluded-owner",
				"gpt-5.1",
				map[int64]struct{}{owner.ID: {}},
				OpenAIUpstreamTransportHTTPSSE,
				OpenAIEndpointCapabilityChatCompletions,
				false,
				OpenAIAccountSchedulingOptions{
					CanTemporarilyOverflow: false,
					Platform:               PlatformOpenAI,
				},
			)

			require.Nil(t, selection)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.ErrorContains(t, err, "canonical_session_owner_excluded")
			require.Zero(t, concurrencyCache.active[provisional.ID])
			require.Contains(t, concurrencyCache.released, provisional.ID)
		})
	}
}
