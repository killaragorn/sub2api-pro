package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountUsageHistoryRepositoryStub struct {
	service.UsageLogRepository
	calls        int
	accountID    int64
	granularity  string
	timezoneName string
	page         int
	pageSize     int
	response     *usagestats.AccountUsageHistoryResponse
}

func (s *accountUsageHistoryRepositoryStub) GetAccountUsageHistory(_ context.Context, accountID int64, granularity, timezoneName string, page, pageSize int) (*usagestats.AccountUsageHistoryResponse, error) {
	s.calls++
	s.accountID = accountID
	s.granularity = granularity
	s.timezoneName = timezoneName
	s.page = page
	s.pageSize = pageSize
	return s.response, nil
}

func newAccountUsageHistoryHandler(repo service.UsageLogRepository) *AccountHandler {
	usageService := service.NewAccountUsageService(
		nil,
		repo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	return &AccountHandler{accountUsageService: usageService}
}

func TestAccountHandlerGetUsageHistoryForwardsValidatedParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &accountUsageHistoryRepositoryStub{
		response: &usagestats.AccountUsageHistoryResponse{
			Items:       []usagestats.AccountUsagePeriod{},
			Granularity: usagestats.AccountUsageGranularityWeek,
			Timezone:    "Asia/Taipei",
			Page:        2,
			PageSize:    25,
			Pages:       1,
		},
	}
	handler := newAccountUsageHistoryHandler(repo)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/accounts/42/usage-history?granularity=week&page=2&page_size=25&timezone=Asia%2FTaipei",
		nil,
	)

	handler.GetUsageHistory(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, int64(42), repo.accountID)
	require.Equal(t, usagestats.AccountUsageGranularityWeek, repo.granularity)
	require.Equal(t, "Asia/Taipei", repo.timezoneName)
	require.Equal(t, 2, repo.page)
	require.Equal(t, 25, repo.pageSize)

	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Granularity string `json:"granularity"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Zero(t, envelope.Code)
	require.Equal(t, usagestats.AccountUsageGranularityWeek, envelope.Data.Granularity)
}

func TestAccountHandlerGetUsageHistoryRejectsInvalidParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		id   string
		url  string
	}{
		{name: "account id", id: "bad", url: "/?timezone=UTC"},
		{name: "negative account id", id: "-1", url: "/?timezone=UTC"},
		{name: "granularity", id: "42", url: "/?granularity=month&timezone=UTC"},
		{name: "page", id: "42", url: "/?page=0&timezone=UTC"},
		{name: "page size", id: "42", url: "/?page_size=101&timezone=UTC"},
		{name: "timezone", id: "42", url: "/?timezone=Not%2FAZone"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &accountUsageHistoryRepositoryStub{}
			handler := newAccountUsageHistoryHandler(repo)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "id", Value: tt.id}}
			c.Request = httptest.NewRequest(http.MethodGet, tt.url, nil)

			handler.GetUsageHistory(c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Zero(t, repo.calls)
		})
	}
}
