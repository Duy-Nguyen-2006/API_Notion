package orchestrator

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Controller struct {
	Router        *Router
	Tools         *Registry
	CtxMgr        *ContextManager
	Executor      ModelClient
	Reasoner      ModelClient
	ReasonerHedge ModelClient
	Responder     *LocalResponder
	Options       OrchestratorOptions
	Metrics       *Metrics
	ResponseCache *ResponseCache
	PrefixCache   *PrefixCache
	CBPool        *CircuitBreakerPool
}

func NewController(root string, reasoner ModelClient, executor ModelClient) *Controller {
	return NewControllerWithOptions(root, reasoner, executor, OrchestratorOptions{MaxConcurrentTools: 8, ToolCacheEnabled: true})
}

func NewControllerWithOptions(root string, reasoner ModelClient, executor ModelClient, opts OrchestratorOptions) *Controller {
	return &Controller{Router: NewRouter(), Tools: NewRegistryWithOptions(root, opts), CtxMgr: NewContextManager(24000), Executor: executor, Reasoner: reasoner, Responder: &LocalResponder{}, Options: opts, Metrics: NewMetrics(), ResponseCache: NewResponseCache(30 * time.Minute), PrefixCache: &PrefixCache{}}
}

func (c *Controller) Run(ctx context.Context, req UserRequest, w StreamWriter) (RunResult, error) {
	if c == nil { return RunResult{}, errors.New("controller is nil") }
	c.ensureDefaults(req.RootDir)
	trace := AgentTrace{StageLatencyMs: map[string]int64{}}
	total := time.Now()
	routerTimer := newStageTimer()
	decision := c.Router.Decide(req)
	trace.StageLatencyMs["router"] = routerTimer.ms()
	trace.Iterations = maxInt(1, decision.MaxIterations)

	if !decision.NeedsTools {
		result, err := c.streamReasoner(ctx, req.Prompt, req.Messages, w, &trace)
		trace.TTFTMs = time.Since(total).Milliseconds()
		c.Metrics.ObserveRun(trace, err)
		return RunResult{Text: result.Text, Trace: trace}, err
	}

	var prefetch <-chan ToolResult
	if decision.Prefetch != nil { prefetch = c.Tools.PrefetchAsync(ctx, decision.Prefetch) }

	if len(decision.DirectToolPlan) > 0 {
		toolTimer := newStageTimer()
		results := c.Tools.RunParallel(ctx, decision.DirectToolPlan)
		if prefetch != nil {
			select { case prefetched, ok := <-prefetch: if ok && prefetched.Content != "" { results = []ToolResult{prefetched}; trace.PrefetchHit = true }; default: }
		}
		trace.StageLatencyMs["tool_exec"] = toolTimer.ms()
		trace.ToolCalls = len(decision.DirectToolPlan)
		if !decision.NeedsReasoner {
			text, err := c.Responder.Stream(ctx, req, results, w)
			trace.TTFTMs = time.Since(total).Milliseconds()
			c.Metrics.ObserveRun(trace, err)
			return RunResult{Text: text, Trace: trace}, err
		}
		distillTimer := newStageTimer()
		distilled := c.CtxMgr.BuildReasonerContext(req, c.CtxMgr.Distill(results))
		trace.StageLatencyMs["context_distill"] = distillTimer.ms()
		result, err := c.streamReasoner(ctx, req.Prompt, distilled, w, &trace)
		trace.TTFTMs = time.Since(total).Milliseconds()
		c.Metrics.ObserveRun(trace, err)
		return RunResult{Text: result.Text, Trace: trace}, err
	}

	if c.Executor == nil { return RunResult{}, errors.New("executor client is required for fallback path") }
	execCb := c.CBPool.Get("executor")
	if cbErr := execCb.Allow(); cbErr != nil { trace.TTFTMs = time.Since(total).Milliseconds(); c.Metrics.ObserveRun(trace, cbErr); return RunResult{}, cbErr }
	execCtx, execCancel := WithStageTimeout(ctx, c.Options.ExecutorTimeout)
	defer execCancel()
	execTimer := newStageTimer()
	var execResult ModelResult
	retryErr := RetryWithBackoff(execCtx, c.Options.RetryMaxAttempts, c.Options.RetryBaseDelay, func() error {
		var chatErr error
		execResult, chatErr = c.Executor.Chat(execCtx, req.Messages)
		return chatErr
	})
	trace.StageLatencyMs["executor_rtt"] = execTimer.ms()
	trace.ModelRoundTrips = 1; trace.ExecutorCalls = 1
	if retryErr != nil { execCb.RecordFailure(); trace.TTFTMs = time.Since(total).Milliseconds(); c.Metrics.ObserveRun(trace, retryErr); return RunResult{Text: execResult.Text, Trace: trace}, retryErr }
	execCb.RecordSuccess()
	if !decision.NeedsReasoner { trace.TTFTMs = time.Since(total).Milliseconds(); c.Metrics.ObserveRun(trace, nil); return RunResult{Text: execResult.Text, Trace: trace}, nil }
	reasonerMessages := c.CtxMgr.BuildReasonerContext(req, []Message{{Role: "system", Content: execResult.Text}})
	result, err := c.streamReasoner(ctx, req.Prompt, reasonerMessages, w, &trace)
	trace.TTFTMs = time.Since(total).Milliseconds()
	c.Metrics.ObserveRun(trace, err)
	return RunResult{Text: result.Text, Trace: trace}, err
}

func (c *Controller) ensureDefaults(root string) {
	if c.Router == nil { c.Router = NewRouter() }
	if c.Tools == nil { c.Tools = NewRegistryWithOptions(root, c.Options) }
	if c.CtxMgr == nil { c.CtxMgr = NewContextManager(24000) }
	if c.Responder == nil { c.Responder = &LocalResponder{} }
	if c.Metrics == nil { c.Metrics = NewMetrics() }
	if c.ResponseCache == nil { c.ResponseCache = NewResponseCache(30 * time.Minute) }
	if c.PrefixCache == nil { c.PrefixCache = &PrefixCache{} }
	if c.CBPool == nil { c.CBPool = newCircuitBreakerPool(c.Options.CBThreshold, c.Options.CBRecovery) }
}

func (c *Controller) streamReasoner(ctx context.Context, query string, messages []Message, w StreamWriter, trace *AgentTrace) (ModelResult, error) {
	if c.Reasoner == nil { return ModelResult{}, errors.New("reasoner client is required") }
	reasonerCb := c.CBPool.Get("reasoner")
	if cbErr := reasonerCb.Allow(); cbErr != nil { return ModelResult{}, cbErr }
	key := c.ResponseCache.Key(query, messages)
	if cached, ok := c.ResponseCache.Get(key); ok {
		if w != nil && cached.Text != "" { _ = w.WriteDelta(cached.Text) }
		trace.StageLatencyMs["reasoner_cache"] = 0
		return cached, nil
	}
	reasonerCtx, reasonerCancel := WithStageTimeout(ctx, c.Options.ReasonerTimeout)
	defer reasonerCancel()
	timer := newStageTimer()
	result, hedged, err := ChatStreamHedged(reasonerCtx, c.Reasoner, c.ReasonerHedge, c.Options.HedgeReasonerAfter, messages, w)
	trace.StageLatencyMs["reasoner_rtt"] += timer.ms()
	trace.ModelRoundTrips++; trace.ReasonerCalls++
	if hedged { trace.ModelRoundTrips++; trace.ReasonerCalls++; trace.StageLatencyMs["reasoner_hedge"] = c.Options.HedgeReasonerAfter.Milliseconds() }
	if err != nil { reasonerCb.RecordFailure() } else { reasonerCb.RecordSuccess(); c.ResponseCache.Set(key, result) }
	return result, err
}

type LocalResponder struct{}
func (r *LocalResponder) Stream(ctx context.Context, req UserRequest, results []ToolResult, w StreamWriter) (string, error) {
	var out strings.Builder
	for _, result := range results { if result.Error != "" { out.WriteString("ERROR: "); out.WriteString(result.Error); out.WriteString("\n"); continue }; out.WriteString(result.Content); if !strings.HasSuffix(result.Content, "\n") { out.WriteString("\n") } }
	text := strings.TrimSpace(out.String())
	if w != nil && text != "" { return text, w.WriteDelta(text) }
	select { case <-ctx.Done(): return text, ctx.Err(); default: return text, nil }
}
