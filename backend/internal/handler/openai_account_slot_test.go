package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAcquireResponsesAccountSlotSharesReleaseGuardWithSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx)

	gatewayService := &service.OpenAIGatewayService{}
	h := &OpenAIGatewayHandler{gatewayService: gatewayService}
	releaseCount := 0
	selection := &service.AccountSelectionResult{
		Account: &service.Account{
			ID:       31,
			Platform: service.PlatformGrok,
		},
		Acquired: true,
		ReleaseFunc: func() {
			releaseCount++
		},
	}
	streamStarted := false

	release, acquired, _ := h.acquireResponsesAccountSlot(
		c,
		nil,
		"",
		selection,
		false,
		false,
		&streamStarted,
		zap.NewNop(),
	)

	require.True(t, acquired)
	require.NotNil(t, release)
	require.NoError(t, gatewayService.RejectAcquiredOpenAISelection(ctx, nil, "", selection))
	require.Equal(t, 1, releaseCount)

	release()
	cancel()
	require.Equal(t, 1, releaseCount)
}

func TestAcquireResponsesAccountSlotReturnsPoolModeSessionHash(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
	selection := &service.AccountSelectionResult{
		Account: &service.Account{
			ID:       32,
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeAPIKey,
			Credentials: map[string]any{
				"pool_mode": true,
			},
		},
		Acquired:    true,
		ReleaseFunc: func() {},
	}
	streamStarted := false

	release, acquired, sessionHash := h.acquireResponsesAccountSlot(
		c,
		nil,
		"",
		selection,
		true,
		false,
		&streamStarted,
		zap.NewNop(),
	)

	require.True(t, acquired)
	require.NotNil(t, release)
	require.NotEmpty(t, sessionHash)
	require.Contains(t, sessionHash, "openai-pool-retry-")
	release()
}
