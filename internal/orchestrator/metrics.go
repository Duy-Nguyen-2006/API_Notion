package orchestrator

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type Metrics struct {
	mu                 sync.Mutex
	ModelRoundTrips    int
	ToolCalls          int
	ReasonerCalls      int
	ExecutorCalls      int
	ToolCacheHits      int
	ReasonerCacheHits  int
	PrefetchHits       int
	TTFTTotalMs        int64
	RunCount           int
	ErrorCount         int
	StageLatencyMs     map[string]int64
	ErrorsByStage      map[string]int
}

type MetricsSnapshot struct {
	ModelRoundTrips    int              `json:"model_round_trips"`
	ToolCalls          int              `json:"tool_calls"`
	ReasonerCalls      int              `json:"reasoner_calls"`
	ExecutorCalls      int              `json:"executor_calls"`
	ToolCacheHits      int              `json:"tool_cache_hits"`
	ReasonerCacheHits  int              `json:"reasoner_cache_hits"`
	PrefetchHits       int              `json:"prefetch_hits"`
	TTFTTotalMs        int64            `json:"ttft_total_ms"`
	RunCount           int              `json:"run_count"`
	ErrorCount         int              `json:"error_count"`
	StageLatencyMs     map[string]int64 `json:"stage_latency_ms"`
	ErrorsByStage      map[string]int   `json:"errors_by_stage"`
}

func NewMetrics() *Metrics { return &Metrics{StageLatencyMs: map[string]int64{}, ErrorsByStage: map[string]int{}} }

func (m *Metrics) AddStage(name string, d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.StageLatencyMs == nil {
		m.StageLatencyMs = map[string]int64{}
	}
	m.StageLatencyMs[name] += d.Milliseconds()
	m.mu.Unlock()
}

// ObserveRun records the outcome of an orchestrator run into cumulative metrics.
func (m *Metrics) ObserveRun(trace AgentTrace, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunCount++
	m.ModelRoundTrips += trace.ModelRoundTrips
	m.ToolCalls += trace.ToolCalls
	m.ReasonerCalls += trace.ReasonerCalls
	m.ExecutorCalls += trace.ExecutorCalls
	if trace.PrefetchHit {
		m.PrefetchHits++
	}
	m.TTFTTotalMs += trace.TTFTMs
	if m.StageLatencyMs == nil {
		m.StageLatencyMs = map[string]int64{}
	}
	for k, v := range trace.StageLatencyMs {
		m.StageLatencyMs[k] += v
	}
	if err != nil {
		m.ErrorCount++
		if m.ErrorsByStage == nil {
			m.ErrorsByStage = map[string]int{}
		}
		stage := classifyErrorStage(trace)
		m.ErrorsByStage[stage]++
	}
}

// classifyErrorStage determines which stage an error is attributed to based on trace data.
func classifyErrorStage(trace AgentTrace) string {
	// If we had tool calls but no reasoner/executor calls, error was in tool stage
	if trace.ToolCalls > 0 && trace.ReasonerCalls == 0 && trace.ExecutorCalls == 0 {
		return "tool"
	}
	// If we had executor calls, error was in executor
	if trace.ExecutorCalls > 0 {
		return "executor"
	}
	// If we had reasoner calls, error was in reasoner
	if trace.ReasonerCalls > 0 {
		return "reasoner"
	}
	// If we got through the router (stage_latency_ms has "router"), error was in routing
	if _, ok := trace.StageLatencyMs["router"]; ok {
		return "router"
	}
	return "unknown"
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	if m == nil {
		return MetricsSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := MetricsSnapshot{
		ModelRoundTrips: m.ModelRoundTrips,
		ToolCalls:       m.ToolCalls,
		ReasonerCalls:   m.ReasonerCalls,
		ExecutorCalls:   m.ExecutorCalls,
		ToolCacheHits:   m.ToolCacheHits,
		ReasonerCacheHits: m.ReasonerCacheHits,
		PrefetchHits:    m.PrefetchHits,
		TTFTTotalMs:     m.TTFTTotalMs,
		RunCount:        m.RunCount,
		ErrorCount:      m.ErrorCount,
		StageLatencyMs:  make(map[string]int64, len(m.StageLatencyMs)),
		ErrorsByStage:   make(map[string]int, len(m.ErrorsByStage)),
	}
	for k, v := range m.StageLatencyMs {
		cp.StageLatencyMs[k] = v
	}
	for k, v := range m.ErrorsByStage {
		cp.ErrorsByStage[k] = v
	}
	return cp
}

// WritePrometheusSnapshot writes all orchestrator metrics in Prometheus text format
// to the provided writer. All metric names are prefixed with "notion2api_orchestrator_".
func WritePrometheusSnapshot(w io.Writer, m *Metrics) {
	if w == nil || m == nil {
		return
	}
	snap := m.Snapshot()
	prefix := "notion2api_orchestrator_"

	// Counters
	fmt.Fprintf(w, "# HELP %srun_total Total orchestrator runs completed.\n", prefix)
	fmt.Fprintf(w, "# TYPE %srun_total counter\n", prefix)
	fmt.Fprintf(w, "%srun_total %d\n", prefix, snap.RunCount)

	fmt.Fprintf(w, "# HELP %serror_total Total orchestrator runs that ended with an error.\n", prefix)
	fmt.Fprintf(w, "# TYPE %serror_total counter\n", prefix)
	fmt.Fprintf(w, "%serror_total %d\n", prefix, snap.ErrorCount)

	fmt.Fprintf(w, "# HELP %smodel_round_trips_total Total model round trips across all orchestrator runs.\n", prefix)
	fmt.Fprintf(w, "# TYPE %smodel_round_trips_total counter\n", prefix)
	fmt.Fprintf(w, "%smodel_round_trips_total %d\n", prefix, snap.ModelRoundTrips)

	fmt.Fprintf(w, "# HELP %stool_calls_total Total tool calls across all orchestrator runs.\n", prefix)
	fmt.Fprintf(w, "# TYPE %stool_calls_total counter\n", prefix)
	fmt.Fprintf(w, "%stool_calls_total %d\n", prefix, snap.ToolCalls)

	fmt.Fprintf(w, "# HELP %sreasoner_calls_total Total reasoner calls across all orchestrator runs.\n", prefix)
	fmt.Fprintf(w, "# TYPE %sreasoner_calls_total counter\n", prefix)
	fmt.Fprintf(w, "%sreasoner_calls_total %d\n", prefix, snap.ReasonerCalls)

	fmt.Fprintf(w, "# HELP %sexecutor_calls_total Total executor calls across all orchestrator runs.\n", prefix)
	fmt.Fprintf(w, "# TYPE %sexecutor_calls_total counter\n", prefix)
	fmt.Fprintf(w, "%sexecutor_calls_total %d\n", prefix, snap.ExecutorCalls)

	fmt.Fprintf(w, "# HELP %sprefetch_hits_total Total prefetch cache hits across all orchestrator runs.\n", prefix)
	fmt.Fprintf(w, "# TYPE %sprefetch_hits_total counter\n", prefix)
	fmt.Fprintf(w, "%sprefetch_hits_total %d\n", prefix, snap.PrefetchHits)

	fmt.Fprintf(w, "# HELP %stool_cache_hits_total Total tool cache hits across all orchestrator runs.\n", prefix)
	fmt.Fprintf(w, "# TYPE %stool_cache_hits_total counter\n", prefix)
	fmt.Fprintf(w, "%stool_cache_hits_total %d\n", prefix, snap.ToolCacheHits)

	fmt.Fprintf(w, "# HELP %sreasoner_cache_hits_total Total reasoner cache hits across all orchestrator runs.\n", prefix)
	fmt.Fprintf(w, "# TYPE %sreasoner_cache_hits_total counter\n", prefix)
	fmt.Fprintf(w, "%sreasoner_cache_hits_total %d\n", prefix, snap.ReasonerCacheHits)

	// TTFT gauge (total ms accumulated)
	fmt.Fprintf(w, "# HELP %sttft_total_ms Cumulative time-to-first-token in milliseconds.\n", prefix)
	fmt.Fprintf(w, "# TYPE %sttft_total_ms counter\n", prefix)
	fmt.Fprintf(w, "%sttft_total_ms %d\n", prefix, snap.TTFTTotalMs)

	// Stage latency gauges
	fmt.Fprintf(w, "# HELP %sstage_latency_ms Cumulative latency per stage in milliseconds.\n", prefix)
	fmt.Fprintf(w, "# TYPE %sstage_latency_ms gauge\n", prefix)
	stageKeys := make([]string, 0, len(snap.StageLatencyMs))
	for k := range snap.StageLatencyMs {
		stageKeys = append(stageKeys, k)
	}
	sort.Strings(stageKeys)
	for _, k := range stageKeys {
		fmt.Fprintf(w, "%sstage_latency_ms{stage=\"%s\"} %d\n", prefix, escapeLabel(k), snap.StageLatencyMs[k])
	}

	// Error by stage
	fmt.Fprintf(w, "# HELP %serrors_by_stage_total Error count by stage.\n", prefix)
	fmt.Fprintf(w, "# TYPE %serrors_by_stage_total counter\n", prefix)
	errKeys := make([]string, 0, len(snap.ErrorsByStage))
	for k := range snap.ErrorsByStage {
		errKeys = append(errKeys, k)
	}
	sort.Strings(errKeys)
	for _, k := range errKeys {
		fmt.Fprintf(w, "%serrors_by_stage_total{stage=\"%s\"} %d\n", prefix, escapeLabel(k), snap.ErrorsByStage[k])
	}
}

func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

// HealthSnapshot reports whether the orchestrator's key clients are configured.
// This is a local check (no network probing).
type HealthSnapshot struct {
	ReasonerReady bool `json:"reasoner_ready"`
	ExecutorReady bool `json:"executor_ready"`
}

// OrchestratorHealthFromController returns a HealthSnapshot indicating whether
// the controller has reasoner and executor clients set.
func OrchestratorHealthFromController(c *Controller) HealthSnapshot {
	if c == nil {
		return HealthSnapshot{}
	}
	return HealthSnapshot{
		ReasonerReady: c.Reasoner != nil,
		ExecutorReady: c.Executor != nil,
	}
}

type TraceRecorder struct { Trace AgentTrace }
func NewTraceRecorder(traceID string) *TraceRecorder { return &TraceRecorder{Trace: AgentTrace{TraceID: traceID, StageLatencyMs: map[string]int64{}}} }
func (r *TraceRecorder) Stage(name string, d time.Duration) { if r == nil { return }; if r.Trace.StageLatencyMs == nil { r.Trace.StageLatencyMs = map[string]int64{} }; r.Trace.StageLatencyMs[name] = d.Milliseconds() }
