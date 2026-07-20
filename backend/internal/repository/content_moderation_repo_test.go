package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildContentModerationLogWhere_BlockedIncludesAllBlockActions(t *testing.T) {
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{Result: "blocked"})

	require.Empty(t, args)
	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.action IN ('block', 'keyword_block', 'hash_block')")
	require.NotContains(t, sql, "l.action = 'block'")
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesHashBlock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND action <> 'hash_block'")).
		WithArgs(int64(1001), since, false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, false)

	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesCyberPolicyWhenRequested(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND ($3::bool IS FALSE OR action <> 'cyber_policy')")).
		WithArgs(int64(1001), since, true).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, true)

	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetCyberPolicyRequestAudit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	mock.ExpectQuery("SELECT id, request_id, cyber_request_protocol").
		WithArgs(int64(77)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "request_id", "cyber_request_protocol", "cyber_request_snapshot",
			"cyber_request_original_bytes", "cyber_request_stored_bytes", "cyber_request_truncated",
		}).AddRow(77, "req-77", "openai_responses", `{"input":"audit"}`, 128, 17, true))

	item, err := repo.GetCyberPolicyRequestAudit(context.Background(), 77)

	require.NoError(t, err)
	require.Equal(t, int64(77), item.LogID)
	require.Equal(t, "req-77", item.RequestID)
	require.Equal(t, "openai_responses", item.Protocol)
	require.True(t, item.Truncated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetCyberPolicyRequestAudit_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	mock.ExpectQuery("SELECT id, request_id, cyber_request_protocol").
		WithArgs(int64(88)).
		WillReturnError(sql.ErrNoRows)

	item, err := repo.GetCyberPolicyRequestAudit(context.Background(), 88)

	require.Nil(t, item)
	require.ErrorIs(t, err, service.ErrCyberPolicyRequestAuditNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
