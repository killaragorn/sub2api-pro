package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type affinityCapacityAccountRepoStub struct {
	AccountRepository
	account          *Account
	accounts         []*Account
	updateExtraCalls int
	bulkUpdateCalls  int
	bulkUpdate       AccountBulkUpdate
}

func (r *affinityCapacityAccountRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *affinityCapacityAccountRepoStub) UpdateExtra(context.Context, int64, map[string]any) error {
	r.updateExtraCalls++
	return nil
}

func (r *affinityCapacityAccountRepoStub) GetByIDs(context.Context, []int64) ([]*Account, error) {
	if r.accounts != nil {
		return r.accounts, nil
	}
	return []*Account{r.account}, nil
}

func (r *affinityCapacityAccountRepoStub) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.bulkUpdateCalls++
	r.bulkUpdate = updates
	return int64(len(ids)), nil
}

func TestAccountAffinityConcurrencyReserveLimits(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Concurrency: 10,
		Extra: map[string]any{
			AccountExtraAffinityConcurrencyReserve: json.Number("3"),
		},
	}

	require.Equal(t, 3, account.GetAffinityConcurrencyReserve())
	require.Equal(t, 7, account.GeneralConcurrencyLimit())

	account.Extra[AccountExtraAffinityConcurrencyReserve] = float64(4)
	require.Equal(t, 4, account.GetAffinityConcurrencyReserve())
	require.Equal(t, 6, account.GeneralConcurrencyLimit())

	account.Extra[AccountExtraAffinityConcurrencyReserve] = 99
	require.Equal(t, 9, account.GetAffinityConcurrencyReserve(), "legacy invalid rows are clamped to preserve one general slot")
	require.Equal(t, 1, account.GeneralConcurrencyLimit())

	account.Extra[AccountExtraAffinityConcurrencyReserve] = -2
	require.Zero(t, account.GetAffinityConcurrencyReserve())
	require.Equal(t, 10, account.GeneralConcurrencyLimit())

	account.Concurrency = 0
	account.Extra[AccountExtraAffinityConcurrencyReserve] = 3
	require.Zero(t, account.GetAffinityConcurrencyReserve())
	require.Zero(t, account.GeneralConcurrencyLimit())

	account.Concurrency = 10
	account.Platform = PlatformAnthropic
	require.Zero(t, account.GetAffinityConcurrencyReserve())
	require.Equal(t, 10, account.GeneralConcurrencyLimit())
}

func TestValidateAccountAffinityConcurrencyReserve(t *testing.T) {
	tests := []struct {
		name        string
		platform    string
		concurrency int
		value       any
		wantError   string
	}{
		{name: "valid", platform: PlatformOpenAI, concurrency: 10, value: 3},
		{name: "zero", platform: PlatformOpenAI, concurrency: 10, value: 0},
		{name: "numeric JSON value", platform: PlatformOpenAI, concurrency: 10, value: float64(3)},
		{name: "negative", platform: PlatformOpenAI, concurrency: 10, value: -1, wantError: "non-negative integer"},
		{name: "fraction", platform: PlatformOpenAI, concurrency: 10, value: 1.5, wantError: "non-negative integer"},
		{name: "equal to concurrency", platform: PlatformOpenAI, concurrency: 3, value: 3, wantError: "less than concurrency"},
		{name: "greater than concurrency", platform: PlatformOpenAI, concurrency: 3, value: 4, wantError: "less than concurrency"},
		{name: "unlimited concurrency", platform: PlatformOpenAI, concurrency: 0, value: 1, wantError: "must be 0 when concurrency is unlimited"},
		{name: "non OpenAI", platform: PlatformAnthropic, concurrency: 10, value: 1, wantError: "only supported for OpenAI accounts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAccountAffinityConcurrencyReserve(tt.platform, tt.concurrency, map[string]any{
				AccountExtraAffinityConcurrencyReserve: tt.value,
			})
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestAffinityConcurrencyReserveValueRejectsUnsignedOverflow(t *testing.T) {
	maxInt := uint64(^uint(0) >> 1)

	value, ok := affinityConcurrencyReserveValue(maxInt)
	require.True(t, ok)
	require.Equal(t, int(maxInt), value)

	_, ok = affinityConcurrencyReserveValue(maxInt + 1)
	require.False(t, ok)
}

func TestAdminServiceUpdateAccountExtraValidatesAffinityConcurrencyReserve(t *testing.T) {
	tests := []struct {
		name        string
		account     *Account
		value       any
		wantCode    int
		wantUpdates int
	}{
		{
			name: "valid OpenAI reserve",
			account: &Account{
				ID:          1,
				Platform:    PlatformOpenAI,
				Concurrency: 5,
			},
			value:       2,
			wantUpdates: 1,
		},
		{
			name: "reserve reaches concurrency",
			account: &Account{
				ID:          1,
				Platform:    PlatformOpenAI,
				Concurrency: 5,
			},
			value:    5,
			wantCode: http.StatusBadRequest,
		},
		{
			name: "fractional reserve",
			account: &Account{
				ID:          1,
				Platform:    PlatformOpenAI,
				Concurrency: 5,
			},
			value:    1.5,
			wantCode: http.StatusBadRequest,
		},
		{
			name: "reserve on another platform",
			account: &Account{
				ID:          1,
				Platform:    PlatformAnthropic,
				Concurrency: 5,
			},
			value:    1,
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &affinityCapacityAccountRepoStub{account: tt.account}
			svc := &adminServiceImpl{accountRepo: repo}

			err := svc.UpdateAccountExtra(context.Background(), tt.account.ID, map[string]any{
				AccountExtraAffinityConcurrencyReserve: tt.value,
			})

			if tt.wantCode == 0 {
				require.NoError(t, err)
			} else {
				require.Equal(t, tt.wantCode, infraerrors.Code(err))
			}
			require.Equal(t, tt.wantUpdates, repo.updateExtraCalls)
		})
	}
}

func TestAdminServiceBulkUpdateAccountsValidatesAffinityReserveAgainstEveryTarget(t *testing.T) {
	repo := &affinityCapacityAccountRepoStub{
		accounts: []*Account{
			{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 5},
			{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2},
		Extra: map[string]any{
			AccountExtraAffinityConcurrencyReserve: 4,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.Success)
	require.Equal(t, 1, repo.bulkUpdateCalls)

	repo.bulkUpdateCalls = 0
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2},
		Extra: map[string]any{
			AccountExtraAffinityConcurrencyReserve: 5,
		},
	})
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Zero(t, repo.bulkUpdateCalls)
}

func TestAdminServiceBulkUpdateAccountsValidatesExistingReserveWhenConcurrencyChanges(t *testing.T) {
	repo := &affinityCapacityAccountRepoStub{
		accounts: []*Account{{
			ID:          1,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Concurrency: 8,
			Extra: map[string]any{
				AccountExtraAffinityConcurrencyReserve: 4,
			},
		}},
	}
	svc := &adminServiceImpl{accountRepo: repo}
	concurrency := 4

	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs:  []int64{1},
		Concurrency: &concurrency,
	})
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Zero(t, repo.bulkUpdateCalls)
}

func TestAdminServiceBulkUpdateAccountsPersistsEffectiveConcurrency(t *testing.T) {
	t.Run("all Grok OAuth targets normalize zero to one", func(t *testing.T) {
		repo := &affinityCapacityAccountRepoStub{
			accounts: []*Account{
				{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 2},
				{ID: 2, Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 3},
			},
		}
		svc := &adminServiceImpl{accountRepo: repo}
		concurrency := 0

		result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs:  []int64{1, 2},
			Concurrency: &concurrency,
		})
		require.NoError(t, err)
		require.Equal(t, 2, result.Success)
		require.NotNil(t, repo.bulkUpdate.Concurrency)
		require.Equal(t, 1, *repo.bulkUpdate.Concurrency)
	})

	t.Run("mixed targets with different effective values are rejected", func(t *testing.T) {
		repo := &affinityCapacityAccountRepoStub{
			accounts: []*Account{
				{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 2},
				{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 3},
			},
		}
		svc := &adminServiceImpl{accountRepo: repo}
		concurrency := 0

		_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs:  []int64{1, 2},
			Concurrency: &concurrency,
		})
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		require.Zero(t, repo.bulkUpdateCalls)
	})
}
