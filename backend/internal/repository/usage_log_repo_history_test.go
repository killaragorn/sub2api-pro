package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestGetAccountUsageHistoryReturnsPaginatedDailyTotals(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &usageLogRepository{sql: db}
	periodStart := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	firstPeriod := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	lastPeriod := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	rows := sqlmock.NewRows([]string{
		"period_start",
		"period_end",
		"requests",
		"tokens",
		"standard_cost",
		"account_cost",
		"user_cost",
		"total_periods",
		"total_requests",
		"total_tokens",
		"total_standard_cost",
		"total_account_cost",
		"total_user_cost",
		"first_period_start",
		"last_period_start",
	}).AddRow(
		periodStart,
		periodStart,
		int64(3),
		int64(1200),
		1.25,
		0.8,
		1.5,
		int64(5),
		int64(12),
		int64(4500),
		4.75,
		3.2,
		5.6,
		firstPeriod,
		lastPeriod,
	)

	mock.ExpectQuery(`(?s)WITH grouped AS .*LEFT JOIN paged p ON TRUE`).
		WithArgs(int64(42), usagestats.AccountUsageGranularityDay, "Asia/Taipei", 2, 2).
		WillReturnRows(rows)

	got, err := repo.GetAccountUsageHistory(
		context.Background(),
		42,
		usagestats.AccountUsageGranularityDay,
		"Asia/Taipei",
		2,
		2,
	)
	require.NoError(t, err)
	require.Equal(t, int64(5), got.Total)
	require.Equal(t, 2, got.Page)
	require.Equal(t, 2, got.PageSize)
	require.Equal(t, 3, got.Pages)
	require.Equal(t, "Asia/Taipei", got.Timezone)
	require.Equal(t, "2026-07-26", got.Summary.FirstPeriodStart)
	require.Equal(t, "2026-07-31", got.Summary.LastPeriodEnd)
	require.InDelta(t, 3.2, got.Summary.TotalAccountCost, 1e-9)
	require.Len(t, got.Items, 1)
	require.Equal(t, "2026-07-30", got.Items[0].PeriodStart)
	require.Equal(t, "2026-07-30", got.Items[0].PeriodEnd)
	require.Equal(t, int64(1200), got.Items[0].Tokens)
	require.InDelta(t, 0.8, got.Items[0].AccountCost, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountUsageHistoryReturnsEmptyWeeklyHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &usageLogRepository{sql: db}
	rows := sqlmock.NewRows([]string{
		"period_start",
		"period_end",
		"requests",
		"tokens",
		"standard_cost",
		"account_cost",
		"user_cost",
		"total_periods",
		"total_requests",
		"total_tokens",
		"total_standard_cost",
		"total_account_cost",
		"total_user_cost",
		"first_period_start",
		"last_period_start",
	}).AddRow(
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		int64(0),
		int64(0),
		int64(0),
		0.0,
		0.0,
		0.0,
		nil,
		nil,
	)

	mock.ExpectQuery(`(?s)WITH grouped AS .*LEFT JOIN paged p ON TRUE`).
		WithArgs(int64(7), usagestats.AccountUsageGranularityWeek, "UTC", 50, 0).
		WillReturnRows(rows)

	got, err := repo.GetAccountUsageHistory(
		context.Background(),
		7,
		usagestats.AccountUsageGranularityWeek,
		"UTC",
		1,
		50,
	)
	require.NoError(t, err)
	require.Empty(t, got.Items)
	require.Equal(t, int64(0), got.Total)
	require.Equal(t, 1, got.Pages)
	require.Empty(t, got.Summary.FirstPeriodStart)
	require.Empty(t, got.Summary.LastPeriodEnd)
	require.NoError(t, mock.ExpectationsWereMet())
}
