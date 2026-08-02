package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListErrorLogsIncludesSLAClassificationOnRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	repo := &opsRepository{db: db}
	createdAt := time.Date(2026, time.August, 2, 8, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`(?s)SELECT COUNT\(\*\) FROM ops_error_logs e`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	columns := []string{
		"id", "created_at", "error_phase", "error_type", "error_owner", "error_source", "severity", "status_code",
		"platform", "model", "resolved", "resolved_at", "resolved_by_user_id", "resolved_by_user_name",
		"client_request_id", "request_id", "error_message", "user_id", "user_email", "api_key_id", "account_id",
		"account_name", "group_id", "group_name", "client_ip", "request_path", "stream", "inbound_endpoint",
		"upstream_endpoint", "requested_model", "upstream_model", "user_agent", "request_type", "is_business_limited",
		"is_sla_excluded", "sla_exclusion_reason", "api_key_name", "api_key_deleted_at",
	}
	mock.ExpectQuery(`(?s)SELECT.*COALESCE\(e.is_business_limited, false\).*COALESCE\(e.is_sla_excluded, false\).*COALESCE\(e.sla_exclusion_reason, ''\).*FROM ops_error_logs e`).
		WithArgs(20, 0).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(
			int64(1), createdAt, "request", "invalid_request_error", "client", "gateway", "P2", int64(200),
			"openai", "gpt-5", false, nil, nil, "", "client-1", "request-1", "blocked", int64(7),
			"user@example.com", int64(8), nil, "", nil, "", nil, "/v1/responses", true, "responses",
			"responses", "gpt-5", "gpt-5", "codex", int64(2), false, true, "prompt_guard_blocked", "key-a", nil,
		))

	out, err := repo.ListErrorLogs(context.Background(), &service.OpsErrorLogFilter{View: "errors"})
	require.NoError(t, err)
	require.Len(t, out.Errors, 1)
	require.False(t, out.Errors[0].IsBusinessLimited)
	require.True(t, out.Errors[0].IsSLAExcluded)
	require.Equal(t, "prompt_guard_blocked", out.Errors[0].SLAExclusionReason)
	require.NoError(t, mock.ExpectationsWereMet())
	mock.ExpectClose()
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}
