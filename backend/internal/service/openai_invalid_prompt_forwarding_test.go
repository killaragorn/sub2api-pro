package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func invalidPromptResponsesSSE() string {
	return strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_invalid_prompt"}}`,
		"",
		"event: response.failed",
		`data: {"type":"response.failed","response":{"id":"resp_invalid_prompt","object":"response","status":"failed","output":[],"error":{"type":"invalid_request_error","code":"invalid_prompt","message":"This session is blocked by keyword policy, please start a new session"}}}`,
		"",
	}, "\n")
}

func invalidPromptUpstreamResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(invalidPromptResponsesSSE())),
	}
}

func requireInvalidPromptForwardedWithoutFailover(t *testing.T, recorder *httptest.ResponseRecorder, c *gin.Context, err error) {
	t.Helper()
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "invalid_prompt must be terminal and must not switch accounts")
	require.Contains(t, recorder.Body.String(), `"code":"invalid_prompt"`)
	require.Contains(t, recorder.Body.String(), "This session is blocked by keyword policy")
	require.Nil(t, GetOpsCyberPolicy(c), "keyword policy must not be classified as cyber_policy")
}

func TestStreamingInvalidPromptPassesThroughWithoutFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	_, err := svc.handleStreamingResponse(
		context.Background(),
		invalidPromptUpstreamResponse(),
		c,
		&Account{ID: 1, Platform: PlatformOpenAI, Name: "upstream-pro"},
		time.Now(),
		"gpt-5",
		"gpt-5",
	)

	requireInvalidPromptForwardedWithoutFailover(t, recorder, c, err)
}

func TestStreamingPassthroughInvalidPromptPassesThroughWithoutFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	_, err := svc.handleStreamingResponsePassthrough(
		context.Background(),
		invalidPromptUpstreamResponse(),
		c,
		&Account{ID: 1, Platform: PlatformOpenAI, Name: "upstream-pro"},
		time.Now(),
		"gpt-5",
		"gpt-5",
	)

	requireInvalidPromptForwardedWithoutFailover(t, recorder, c, err)
}
