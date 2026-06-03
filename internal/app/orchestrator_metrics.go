package app

import (
	"io"

	"notion2api/internal/orchestrator"
)

// globalOrchestratorMetrics is a shared Metrics instance used by all orchestrator
// controllers created per-request. It accumulates metrics across the lifetime of
// the process.
var globalOrchestratorMetrics = orchestrator.NewMetrics()

// orchestratorMetricsWriter is the hook called by writePrometheusMetrics to append
// orchestrator-specific Prometheus lines. It is set at init time.
var orchestratorMetricsWriter func(io.Writer)

// orchestratorHealthzHook returns additional key-value pairs to merge into the
// /healthz JSON response. When non-nil, it is called by serveHealthz.
var orchestratorHealthzHook func() map[string]any

func init() {
	orchestratorMetricsWriter = func(w io.Writer) {
		orchestrator.WritePrometheusSnapshot(w, globalOrchestratorMetrics)
	}
	orchestratorHealthzHook = func() map[string]any {
		return map[string]any{
			"orchestrator_reasoner_ready": false,
			"orchestrator_executor_ready": false,
		}
	}
}
