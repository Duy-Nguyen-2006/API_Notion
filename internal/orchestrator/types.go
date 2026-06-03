package orchestrator

import (
	"context"
	"time"
)

type Message struct {
	Role    string
	Content string
}

type UserRequest struct {
	Prompt  string
	Messages []Message
	RootDir string
}

type Complexity string

const (
	ComplexityLow    Complexity = "low"
	ComplexityMedium Complexity = "medium"
	ComplexityHigh   Complexity = "high"
)

type Intent string

const (
	IntentSimpleChat      Intent = "simple_chat"
	IntentFileRead        Intent = "file_read"
	IntentFileReadSummary Intent = "file_read_summary"
	IntentFileEdit        Intent = "file_edit"
	IntentListDir         Intent = "list_dir"
	IntentSearch          Intent = "search"
	IntentCodeAnalysis    Intent = "code_analysis"
)

type ToolCall struct {
	Name string
	Args map[string]any
}

// OrchestratorOptions are request-level/default orchestrator knobs. They are kept
// local to this package to avoid widening AppConfig while Phase 2/3 primitives
// are still being integrated by the app layer.
type OrchestratorOptions struct {
	MaxConcurrentTools  int
	RateLimitPerSecond  int
	HedgeReasonerAfter  time.Duration
	ExecuteEnabled      bool
	ToolCacheEnabled    bool
	CBThreshold         int
	CBRecovery          time.Duration
	RetryMaxAttempts    int
	RetryBaseDelay      time.Duration
	ExecutorTimeout     time.Duration
	ReasonerTimeout     time.Duration
}

type PrefetchHint struct {
	Paths []string
}

type Decision struct {
	Intent         Intent
	Complexity     Complexity
	NeedsTools     bool
	NeedsReasoner  bool
	Tools          []string
	MaxIterations  int
	Prefetch       *PrefetchHint
	DirectToolPlan []ToolCall
}

type ToolResult struct {
	Name     string
	Call     ToolCall
	Content  string
	Metadata map[string]any
	Error    string
}

type ModelResult struct {
	Text string
}

type AgentTrace struct {
	TraceID         string           `json:"trace_id,omitempty"`
	Iterations      int              `json:"iterations"`
	ModelRoundTrips int              `json:"model_round_trips"`
	ToolCalls       int              `json:"tool_calls"`
	ReasonerCalls   int              `json:"reasoner_calls"`
	ExecutorCalls   int              `json:"executor_calls"`
	PrefetchHit     bool             `json:"prefetch_hit"`
	TTFTMs          int64            `json:"ttft_ms"`
	StageLatencyMs  map[string]int64 `json:"stage_latency_ms"`
}

type RunResult struct {
	Text  string
	Trace AgentTrace
}

type StreamWriter interface {
	WriteDelta(string) error
}

type ModelClient interface {
	Chat(ctx context.Context, messages []Message) (ModelResult, error)
	ChatStream(ctx context.Context, messages []Message, w StreamWriter) (ModelResult, error)
}

type stageTimer struct {
	start time.Time
}

func newStageTimer() stageTimer {
	return stageTimer{start: time.Now()}
}

func (t stageTimer) ms() int64 {
	return time.Since(t.start).Milliseconds()
}
