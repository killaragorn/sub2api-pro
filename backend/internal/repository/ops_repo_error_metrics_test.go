package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQueryErrorCountsSeparatesOperationalAndSLAScopes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	repo := &opsRepository{db: db}
	start := time.Date(2026, time.August, 2, 7, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectQuery(`(?s)error_type = 'cyber_policy'.*COALESCE\(is_sla_excluded, false\) = true.*AS error_total.*COALESCE\(status_code, 0\) >= 400 AND NOT is_business_limited AND NOT is_sla_excluded.*AS error_sla`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"error_total", "business_limited", "error_sla", "upstream_excl", "upstream_429", "upstream_529",
		}).AddRow(int64(7), int64(2), int64(3), int64(1), int64(0), int64(0)))

	total, business, sla, _, _, _, err := repo.queryErrorCounts(context.Background(), &service.OpsDashboardFilter{}, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(7), total)
	require.Equal(t, int64(2), business)
	require.Equal(t, int64(3), sla)
	require.NoError(t, mock.ExpectationsWereMet())
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertHourlyMetricsPersistsOperationalErrorCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	repo := &opsRepository{db: db}
	start := time.Date(2026, time.August, 2, 6, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	mock.ExpectExec(`(?s)AS is_operational_error.*COUNT\(\*\) FILTER \(WHERE is_operational_error\) AS error_count_total.*COUNT\(\*\) FILTER \(WHERE is_business_limited\) AS business_limited_count`).
		WithArgs(start, end).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.UpsertHourlyMetrics(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetErrorDistributionKeepsOperationalErrorsButNotInSLACount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	repo := &opsRepository{db: db}
	start := time.Date(2026, time.August, 2, 7, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	filter := &service.OpsDashboardFilter{StartTime: start, EndTime: end}

	mock.ExpectQuery(`(?s)COUNT\(\*\) FILTER \(WHERE COALESCE\(status_code, 0\) >= 400 AND NOT is_business_limited AND NOT is_sla_excluded\) AS sla.*error_type = 'cyber_policy'.*COALESCE\(is_sla_excluded, false\) = true`).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"status_code", "total", "sla", "business_limited"}).
			AddRow(200, int64(4), int64(0), int64(0)))

	out, err := repo.GetErrorDistribution(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, int64(4), out.Total)
	require.Len(t, out.Items, 1)
	require.Equal(t, int64(0), out.Items[0].SLA)
	require.NoError(t, mock.ExpectationsWereMet())
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}
