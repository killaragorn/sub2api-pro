package securityaudit

import (
	"crypto/sha256"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const latencySampleCapacity = 2048

type AtomicMetrics struct {
	total        atomic.Int64
	allowed      atomic.Int64
	flagged      atomic.Int64
	blocked      atomic.Int64
	unavailable  atomic.Int64
	invalid      atomic.Int64
	timeouts     atomic.Int64
	failovers    atomic.Int64
	bulkheadFull atomic.Int64
	recordFailed atomic.Int64
	latencyTotal atomic.Int64
	latencyMax   atomic.Int64
	enqueued     atomic.Int64
	dropped      atomic.Int64
	latencyMu    sync.RWMutex
	latencies    []int64
	latencyNext  int
	endpointMu   sync.RWMutex
	endpoints    map[endpointMetricKey]*endpointMetricState
}

type endpointMetricKey struct {
	endpointID     string
	credentialHash [sha256.Size]byte
}

type endpointMetricState struct {
	active         int64
	total          int64
	success        int64
	errors         int64
	latencyTotalMS int64
	lastLatencyMS  int64
	lastHTTPStatus int
	lastErrorCode  string
	lastSucceeded  bool
}

func NewAtomicMetrics() *AtomicMetrics {
	return &AtomicMetrics{endpoints: make(map[endpointMetricKey]*endpointMetricState)}
}

func (m *AtomicMetrics) Snapshot() GuardMetricsSnapshot {
	if m == nil {
		return GuardMetricsSnapshot{}
	}
	snapshot := GuardMetricsSnapshot{
		Total: m.total.Load(), Allowed: m.allowed.Load(), Flagged: m.flagged.Load(),
		Blocked: m.blocked.Load(), Unavailable: m.unavailable.Load(), Invalid: m.invalid.Load(),
		Timeouts: m.timeouts.Load(), Failovers: m.failovers.Load(), BulkheadFull: m.bulkheadFull.Load(),
		RecordFailed: m.recordFailed.Load(), LatencyCount: m.total.Load(), LatencyMaxMS: m.latencyMax.Load(),
	}
	if snapshot.LatencyCount > 0 {
		snapshot.LatencyAvgMS = m.latencyTotal.Load() / snapshot.LatencyCount
	}
	m.latencyMu.RLock()
	samples := append([]int64(nil), m.latencies...)
	m.latencyMu.RUnlock()
	if len(samples) > 0 {
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		snapshot.LatencyP50MS = percentile(samples, 0.50)
		snapshot.LatencyP95MS = percentile(samples, 0.95)
		snapshot.LatencyP99MS = percentile(samples, 0.99)
	}
	return snapshot
}

func (m *AtomicMetrics) AuditSnapshot() AuditMetricsSnapshot {
	if m == nil {
		return AuditMetricsSnapshot{}
	}
	return AuditMetricsSnapshot{Enqueued: m.enqueued.Load(), Dropped: m.dropped.Load()}
}

func (m *AtomicMetrics) Observe(kind DecisionKind, latency time.Duration) {
	if m == nil {
		return
	}
	m.total.Add(1)
	latencyMS := latency.Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}
	m.latencyTotal.Add(latencyMS)
	for current := m.latencyMax.Load(); latencyMS > current && !m.latencyMax.CompareAndSwap(current, latencyMS); current = m.latencyMax.Load() {
	}
	m.latencyMu.Lock()
	if len(m.latencies) < latencySampleCapacity {
		m.latencies = append(m.latencies, latencyMS)
	} else {
		m.latencies[m.latencyNext] = latencyMS
		m.latencyNext = (m.latencyNext + 1) % latencySampleCapacity
	}
	m.latencyMu.Unlock()
	switch kind {
	case DecisionFlag:
		m.flagged.Add(1)
	case DecisionBlock:
		m.blocked.Add(1)
	case DecisionUnavailable:
		m.unavailable.Add(1)
	case DecisionInvalid:
		m.invalid.Add(1)
	default:
		m.allowed.Add(1)
	}
}

func percentile(sorted []int64, quantile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * quantile)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func (m *AtomicMetrics) IncEnqueued() {
	if m != nil {
		m.enqueued.Add(1)
	}
}

func (m *AtomicMetrics) IncDropped() {
	if m != nil {
		m.dropped.Add(1)
	}
}

func (m *AtomicMetrics) IncTimeout() {
	if m != nil {
		m.timeouts.Add(1)
	}
}
func (m *AtomicMetrics) IncFailover() {
	if m != nil {
		m.failovers.Add(1)
	}
}
func (m *AtomicMetrics) IncBulkheadFull() {
	if m != nil {
		m.bulkheadFull.Add(1)
	}
}
func (m *AtomicMetrics) IncRecordFailed() {
	if m != nil {
		m.recordFailed.Add(1)
	}
}

func (m *AtomicMetrics) EndpointStarted(endpoint ActiveEndpoint) {
	if m == nil {
		return
	}
	m.endpointMu.Lock()
	state := m.endpointStateLocked(endpoint)
	state.active++
	m.endpointMu.Unlock()
}

func (m *AtomicMetrics) EndpointFinished(endpoint ActiveEndpoint, latency time.Duration, scanErr error) {
	if m == nil {
		return
	}
	latencyMS := latency.Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}
	httpStatus := httpStatusFromGuardError(scanErr)
	errorCode := guardErrorCodeForEndpointLoad(scanErr)

	m.endpointMu.Lock()
	state := m.endpointStateLocked(endpoint)
	if state.active > 0 {
		state.active--
	}
	state.total++
	state.latencyTotalMS += latencyMS
	state.lastLatencyMS = latencyMS
	state.lastHTTPStatus = httpStatus
	state.lastErrorCode = errorCode
	state.lastSucceeded = scanErr == nil
	if scanErr == nil {
		state.success++
	} else {
		state.errors++
	}
	m.endpointMu.Unlock()
}

func (m *AtomicMetrics) EndpointLoads(endpoints []ActiveEndpoint) []EndpointLoadSnapshot {
	loads := make([]EndpointLoadSnapshot, 0, len(endpoints))
	if m == nil {
		return loads
	}
	m.endpointMu.RLock()
	defer m.endpointMu.RUnlock()
	for index, endpoint := range endpoints {
		load := EndpointLoadSnapshot{
			Index:         index,
			EndpointID:    endpoint.ID,
			EndpointName:  endpoint.Name,
			Protocol:      endpoint.Protocol,
			Model:         endpoint.Model,
			Enabled:       endpoint.Enabled,
			KeyConfigured: strings.TrimSpace(endpoint.Token) != "",
			MaskedKey:     maskPromptAuditCredential(endpoint.Token),
			Status:        "idle",
		}
		state := m.endpoints[endpointMetricKeyFor(endpoint)]
		if state != nil {
			load.Active = state.active
			load.Total = state.total
			load.Success = state.success
			load.Errors = state.errors
			load.LastLatencyMS = state.lastLatencyMS
			load.LastHTTPStatus = state.lastHTTPStatus
			load.LastErrorCode = state.lastErrorCode
			if state.total > 0 {
				load.AvgLatencyMS = state.latencyTotalMS / state.total
				if state.lastSucceeded {
					load.Status = "healthy"
				} else {
					load.Status = "error"
				}
			}
		}
		if !endpoint.Enabled {
			load.Status = "disabled"
		}
		loads = append(loads, load)
	}
	return loads
}

func (m *AtomicMetrics) endpointStateLocked(endpoint ActiveEndpoint) *endpointMetricState {
	if m.endpoints == nil {
		m.endpoints = make(map[endpointMetricKey]*endpointMetricState)
	}
	key := endpointMetricKeyFor(endpoint)
	state := m.endpoints[key]
	if state == nil {
		state = &endpointMetricState{}
		m.endpoints[key] = state
	}
	return state
}

func endpointMetricKeyFor(endpoint ActiveEndpoint) endpointMetricKey {
	return endpointMetricKey{
		endpointID:     strings.TrimSpace(endpoint.ID),
		credentialHash: sha256.Sum256([]byte(strings.TrimSpace(endpoint.Token))),
	}
}

func maskPromptAuditCredential(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return ""
	}
	if len(runes) < 14 {
		return "****"
	}
	return string(runes[:6]) + "****" + string(runes[len(runes)-4:])
}

func httpStatusFromGuardError(scanErr error) int {
	if scanErr == nil {
		return 200
	}
	var guardErr *GuardError
	if errors.As(scanErr, &guardErr) {
		return guardErr.HTTPStatus
	}
	return 0
}

func guardErrorCodeForEndpointLoad(scanErr error) string {
	if scanErr == nil {
		return ""
	}
	var guardErr *GuardError
	if errors.As(scanErr, &guardErr) && strings.TrimSpace(guardErr.Code) != "" {
		return guardErr.Code
	}
	return ErrorCodeUnavailable
}
