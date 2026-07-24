package service

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateOpenAISchedulerSwitches(t *testing.T) {
	for _, settings := range []*SystemSettings{
		{},
		{OpenAIAdvancedSchedulerEnabled: true},
		{OpenAIPrioritySaturationEnabled: true},
	} {
		require.NoError(t, validateOpenAISchedulerSwitches(settings))
	}

	err := validateOpenAISchedulerSwitches(&SystemSettings{
		OpenAIAdvancedSchedulerEnabled:  true,
		OpenAIPrioritySaturationEnabled: true,
	})
	require.ErrorContains(t, err, "cannot both be enabled")
}

func TestRefreshCachedSettingsPreservesPrioritySaturationSwitch(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	service := &SettingService{}
	service.refreshCachedSettings(&SystemSettings{
		OpenAIPrioritySaturationEnabled: true,
	})

	cached, ok := openAIAdvancedSchedulerSettingCache.Load().(*cachedOpenAIAdvancedSchedulerSetting)
	require.True(t, ok)
	require.NotNil(t, cached)
	require.True(t, cached.prioritySaturationEnabled)
	require.False(t, cached.enabled)
}

func TestOpenAIAccountSchedulersShareRuntimeStatsAcrossPolicySwitch(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	svc := &OpenAIGatewayService{}
	expiresAt := time.Now().Add(time.Minute).UnixNano()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:   true,
		expiresAt: expiresAt,
	})
	weighted, ok := svc.getOpenAIAccountScheduler(t.Context()).(*defaultOpenAIAccountScheduler)
	require.True(t, ok)

	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		prioritySaturationEnabled: true,
		expiresAt:                 expiresAt,
	})
	priority, ok := svc.getOpenAIAccountScheduler(t.Context()).(*prioritySaturationOpenAIAccountScheduler)
	require.True(t, ok)

	require.NotNil(t, weighted.stats)
	require.Same(t, weighted.stats, priority.base.stats)
	require.Same(t, weighted.stats, svc.openaiAccountStats)
}

func TestOpenAIAccountRuntimeStatsConcurrentInitialization(t *testing.T) {
	const workers = 64

	svc := &OpenAIGatewayService{}
	start := make(chan struct{})
	results := make(chan *openAIAccountRuntimeStats, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			<-start
			results <- svc.getOpenAIAccountRuntimeStats()
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	var canonical *openAIAccountRuntimeStats
	for stats := range results {
		require.NotNil(t, stats)
		if canonical == nil {
			canonical = stats
			continue
		}
		require.Same(t, canonical, stats)
	}
	require.Same(t, canonical, svc.openaiAccountStats)
}
