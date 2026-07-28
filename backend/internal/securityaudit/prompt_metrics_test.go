package securityaudit

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAtomicMetricsExposeCountsLatencyDistributionAndAsyncDelivery(t *testing.T) {
	metrics := NewAtomicMetrics()
	latencies := []time.Duration{10, 20, 30, 40, 100}
	kinds := []DecisionKind{DecisionAllow, DecisionFlag, DecisionBlock, DecisionUnavailable, DecisionInvalid}
	for index := range latencies {
		metrics.Observe(kinds[index], latencies[index]*time.Millisecond)
	}
	metrics.IncTimeout()
	metrics.IncFailover()
	metrics.IncBulkheadFull()
	metrics.IncRecordFailed()
	metrics.IncEnqueued()
	metrics.IncDropped()

	snapshot := metrics.Snapshot()
	require.Equal(t, int64(5), snapshot.Total)
	require.Equal(t, int64(5), snapshot.LatencyCount)
	require.Equal(t, int64(40), snapshot.LatencyAvgMS)
	require.Equal(t, int64(30), snapshot.LatencyP50MS)
	require.Equal(t, int64(40), snapshot.LatencyP95MS)
	require.Equal(t, int64(40), snapshot.LatencyP99MS)
	require.Equal(t, int64(100), snapshot.LatencyMaxMS)
	require.Equal(t, AuditMetricsSnapshot{Enqueued: 1, Dropped: 1}, metrics.AuditSnapshot())
}

func TestAtomicMetricsConcurrentObservationIsBoundedAndRaceSafe(t *testing.T) {
	metrics := NewAtomicMetrics()
	const observations = 4096
	endpoint := ActiveEndpoint{ID: "guard-concurrent", Name: "Guard concurrent", Token: "gsk_concurrent_credential", Enabled: true}
	var wg sync.WaitGroup
	for index := 0; index < observations; index++ {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			metrics.Observe(DecisionAllow, time.Duration(value%250)*time.Millisecond)
			metrics.EndpointStarted(endpoint)
			metrics.EndpointFinished(endpoint, time.Duration(value%250)*time.Millisecond, nil)
		}(index)
	}
	wg.Wait()
	require.Equal(t, int64(observations), metrics.Snapshot().Total)
	metrics.latencyMu.RLock()
	require.LessOrEqual(t, len(metrics.latencies), latencySampleCapacity)
	metrics.latencyMu.RUnlock()
	loads := metrics.EndpointLoads([]ActiveEndpoint{endpoint})
	require.Len(t, loads, 1)
	require.Zero(t, loads[0].Active)
	require.Equal(t, int64(observations), loads[0].Total)
	require.Equal(t, int64(observations), loads[0].Success)
}

func TestAtomicMetricsExposeOrderedMaskedEndpointKeyLoads(t *testing.T) {
	metrics := NewAtomicMetrics()
	primary := ActiveEndpoint{
		ID: "groq-primary", Name: "Groq primary", Protocol: EndpointProtocolGroqSafeguard,
		Model: DefaultGroqSafeguardModel, Token: "gsk_1234567890abcdef", Enabled: true,
	}
	disabled := ActiveEndpoint{
		ID: "qwen-disabled", Name: "Qwen disabled", Protocol: EndpointProtocolQwen3Guard,
		Model: DefaultGuardModel, Token: "short", Enabled: false,
	}

	metrics.EndpointStarted(primary)
	active := metrics.EndpointLoads([]ActiveEndpoint{primary})
	require.Equal(t, int64(1), active[0].Active)
	require.Equal(t, "idle", active[0].Status)

	metrics.EndpointFinished(primary, 25*time.Millisecond, nil)
	metrics.EndpointStarted(primary)
	metrics.EndpointFinished(primary, 75*time.Millisecond, &GuardError{
		Code: ErrorCodeUnavailable, HTTPStatus: 429, Retryable: true,
	})

	loads := metrics.EndpointLoads([]ActiveEndpoint{disabled, primary})
	require.Len(t, loads, 2)
	require.Equal(t, "qwen-disabled", loads[0].EndpointID)
	require.Equal(t, "disabled", loads[0].Status)
	require.Equal(t, "****", loads[0].MaskedKey)

	load := loads[1]
	require.Equal(t, 1, load.Index)
	require.Equal(t, "groq-primary", load.EndpointID)
	require.Equal(t, "Groq primary", load.EndpointName)
	require.True(t, load.KeyConfigured)
	require.Equal(t, "gsk_12****cdef", load.MaskedKey)
	require.Equal(t, "error", load.Status)
	require.Zero(t, load.Active)
	require.Equal(t, int64(2), load.Total)
	require.Equal(t, int64(1), load.Success)
	require.Equal(t, int64(1), load.Errors)
	require.Equal(t, int64(50), load.AvgLatencyMS)
	require.Equal(t, int64(75), load.LastLatencyMS)
	require.Equal(t, 429, load.LastHTTPStatus)
	require.Equal(t, ErrorCodeUnavailable, load.LastErrorCode)

	raw, err := json.Marshal(loads)
	require.NoError(t, err)
	require.NotContains(t, string(raw), primary.Token)

	replaced := primary
	replaced.Token = "gsk_replacement_credential"
	replacedLoad := metrics.EndpointLoads([]ActiveEndpoint{replaced})
	require.Zero(t, replacedLoad[0].Total, "changing a credential must not inherit the old key's load")
	require.Equal(t, "idle", replacedLoad[0].Status)
}
