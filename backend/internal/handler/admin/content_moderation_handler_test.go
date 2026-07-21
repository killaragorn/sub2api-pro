package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cyberRequestAuditHandlerRepo struct {
	audit *service.CyberPolicyRequestAudit
	err   error
	calls int
}

func (r *cyberRequestAuditHandlerRepo) CreateLog(context.Context, *service.ContentModerationLog) error {
	return nil
}

func (r *cyberRequestAuditHandlerRepo) ListLogs(context.Context, service.ContentModerationLogFilter) ([]service.ContentModerationLog, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *cyberRequestAuditHandlerRepo) GetCyberPolicyRequestAudit(context.Context, int64) (*service.CyberPolicyRequestAudit, error) {
	r.calls++
	return r.audit, r.err
}

func (r *cyberRequestAuditHandlerRepo) CountFlaggedByUserSince(context.Context, int64, time.Time, bool) (int, error) {
	return 0, nil
}

func (r *cyberRequestAuditHandlerRepo) CleanupExpiredLogs(context.Context, time.Time, time.Time) (*service.ContentModerationCleanupResult, error) {
	return &service.ContentModerationCleanupResult{}, nil
}

func (r *cyberRequestAuditHandlerRepo) UpdateLogEmailSent(context.Context, int64, bool) error {
	return nil
}

func (r *cyberRequestAuditHandlerRepo) UpdateCyberPolicyOutcome(context.Context, int64, int, bool, bool) error {
	return nil
}

func TestContentModerationHandlerGetCyberPolicyRequestAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const requestBody = `{"model":"gpt-5","input":[{"role":"user","content":"audit"},{"type":"function_call_output","output":"complete tool output"}]}`
	repo := &cyberRequestAuditHandlerRepo{audit: &service.CyberPolicyRequestAudit{
		LogID:         77,
		RequestID:     "req-77",
		Protocol:      service.ContentModerationProtocolOpenAIResponses,
		RequestBody:   requestBody,
		OriginalBytes: int64(len(requestBody)),
		StoredBytes:   len(requestBody),
		Truncated:     false,
	}}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/logs/:id/cyber-request", handler.GetCyberPolicyRequestAudit)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/logs/77/cyber-request", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Code int                             `json:"code"`
		Data service.CyberPolicyRequestAudit `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Zero(t, response.Code)
	require.Equal(t, *repo.audit, response.Data)
	require.Equal(t, 1, repo.calls)
}

func TestContentModerationHandlerGetCyberPolicyRequestAuditRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cyberRequestAuditHandlerRepo{}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/logs/:id/cyber-request", handler.GetCyberPolicyRequestAudit)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/logs/not-a-number/cyber-request", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, repo.calls)
}
