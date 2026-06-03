# ADR-001 Orchestrator-Reasoner Architecture Decisions

Date: 2026-06-03

## Status

Accepted

## Context

The Notion AI API (`notion.lgmmo.click`) is chat-only — it cannot access the
filesystem, search code, or execute tools. The orchestrator-reasoner
architecture adds a local tool execution layer paired with a secondary executor
model (`mimo-v2.5-pro`) to bridge this gap.

The primary design tension is between **latency** (fewer model round-trips =
faster response) and **flexibility** (letting the model decide routing =
smarter behavior). Several architectural decisions were made to resolve this
tension.

## Decisions

### D1: Local Router + Direct Tool Plan vs Model-Driven Routing

**Decision**: Use a local, rule-based router that decides intent and
synthesizes tool plans upfront, rather than sending the request to an executor
model to decide routing.

**Rationale**:
- Model-driven routing costs 1 model RTT *before* any work begins (500–2000ms).
- A local router runs in ~µs — negligible latency.
- When paths/searches are explicit in the user prompt, the router can
  synthesize a `DirectToolPlan` and skip the executor entirely.
- Model-driven routing is deferred to a future "ML classifier" decision when
  rule accuracy hits a ceiling.

**Alternatives considered**:
1. **Always executor-first**: Send every request to mimo, let it decide tools.
   Rejected: wastes 1 RTT on simple read/list/search requests.
2. **Hybrid (model + local)**: Run local rules first, escalate to model on
   uncertainty. Current design already does this — the executor is the
   escalation path for complex/ambiguous requests.

**Consequences**:
- Simple requests (read, list, search with clear paths) complete in 0 model
  RTTs.
- Ambiguous requests fall back to executor (1 extra RTT) — acceptable tradeoff.
- Classifier rules need tuning over time; Vietnamese+English keyword matching
  has a ~80% accuracy ceiling.

---

### D2: Hedged Reasoner Requests

**Decision**: After `hedge_reasoner_ms` (default 800ms), fire a second
parallel request to the reasoner. Use whichever responds first.

**Rationale**:
- Notion AI (reasoner) has high tail latency (p99 can exceed 10s).
- Hedging trades ~2x cost for the p99 case for significantly better p99
  latency.
- The hedge is only triggered after a delay, so p50 requests (which complete
  quickly) are not affected.
- Cost impact: ~10–20% increase in reasoner API calls (only the slow tail
  triggers hedges).

**Alternatives considered**:
1. **No hedging**: Accept high p99. Rejected: p99 > 15s is unacceptable for
   interactive use.
2. **Aggressive hedging (always 2 requests)**: 2x cost on every request.
   Rejected: cost-prohibitive for the reasoner.
3. **Retry on timeout**: Higher latency than hedging (must wait for full
   timeout before retry).

**Consequences**:
- `hedge_reasoner_ms` must be tuned based on actual reasoner latency
  distribution (see `docs/latency-tuning.md`).
- `agent_trace.reasoner_calls` can be 2 for hedged requests.
- Reasoner API cost increases by ~10–20%.

---

### D3: Execute Hard-Off Until Security Review

**Decision**: The `execute` tool unconditionally rejects all commands with a
structured error. The `enabled` field exists in config but is not checked — the
rejection is hardcoded in `ExecuteTool.Execute()`.

**Rationale**:
- Arbitrary command execution is the highest-risk tool in the system.
- Path traversal, command injection, and sandbox escape are all possible.
- A security review must assess sandbox requirements before enabling.
- The orchestrator's value proposition (read, list, search, edit) does not
  require `execute` for the MVP.

**Alternatives considered**:
1. **Enable with basic sandbox**: Too risky without thorough review.
2. **Remove the tool entirely**: Keep it registered so the router can route to
   it (returns structured rejection), and the code is ready for security
   sign-off.

**Consequences**:
- Users requesting command execution get a clear error message.
- `execute` can be enabled after security review (see `docs/security-review.md`
  for sandbox requirements).
- Some use cases (running tests, building code) are blocked until `execute` is
  enabled.

---

### D4: In-Memory Cache vs Redis (v1 Choice)

**Decision**: Use in-memory caches for tool results, reasoner responses, and
prefix cache in v1. No external cache dependency.

**Rationale**:
- Single-node deployment for v1 — no need for distributed cache.
- In-memory caches are zero-dependency, zero-latency (ns access).
- Three cache types implemented:
  - **ToolCache**: Keyed on `(path, mtime, size)` — invalidated on `edit_file`.
  - **ResponseCache**: Keyed on `hash(query + context)` — TTL-based expiry.
  - **PrefixCache**: Fixed system prompt + tool schema prefix for prefill.
- Redis adds operational complexity (deployment, networking, failure modes) that
  is not justified for a single-node prototype.

**Alternatives considered**:
1. **Redis from day 1**: Adds deployment complexity, network hop for every
   cache read. Deferred to multi-node phase.
2. **No caching**: Rejected — cache hit rate > 50% target requires caching.
3. **SQLite-backed cache**: Rejected — disk I/O slower than in-memory; SQLite
   is already used for conversation persistence.

**Consequences**:
- Cache is lost on restart (acceptable for v1).
- Single-node only — scaling to multiple instances requires Redis migration.
- Memory usage grows with cache size; TTL-based eviction bounds this.

---

### D5: Rule-Based Classifier vs ML

**Decision**: Use rule-based intent classification with keyword matching
(Vietnamese + English) and regex path extraction. No ML model for routing.

**Rationale**:
- Rule-based classification runs in ~µs — no model RTT overhead.
- The router's job is simple: classify into ~7 intents with ~5 tool types.
- Vietnamese+English keyword matching achieves > 80% accuracy on the target
  workload.
- Fallback behavior (tool + reasoner) is safe — worst case is 1 extra RTT.
- ML classifier requires training data, inference infrastructure, and
  adds latency to the routing decision.

**Alternatives considered**:
1. **ML classifier (small model)**: Higher accuracy but adds ~50–100ms latency
   and requires training infrastructure. Deferred.
2. **Executor-only routing**: Rejected — costs 1 RTT on every request.
3. **Zero-classification (always tool + reasoner)**: Simple but wastes
   reasoner calls on pure chat or simple read requests.

**Consequences**:
- Accuracy ceiling ~85% — some requests will be misrouted.
- Fallback behavior mitigates misrouting (adds 1 RTT but doesn't fail).
- Rules need periodic tuning as user patterns evolve.
- ML classifier can be added later as an optional enhancement.

---

## Summary

| Decision | Choice | Impact |
|----------|--------|--------|
| Routing | Local rule-based router | 0 RTT for clear requests |
| Hedging | Delayed parallel reasoner request | p99 ↓, cost ↑ ~15% |
| Execute | Hard-off (unconditional rejection) | Security-first, blocks some use cases |
| Cache | In-memory (Tool + Response + Prefix) | Zero-dependency, single-node |
| Classifier | Rules (Viet+Eng keywords) | µs latency, ~80% accuracy |

## References

- SPEC: `SPEC_ORCHESTRATOR_REASONER.md`
- PLAN: `PLAN_IMPLEMENTATION.md`
- Checklist: `docs/ORCHESTRATOR_CHECKLIST.md`
- Security Review: `docs/security-review.md`
- Latency Tuning: `docs/latency-tuning.md`
