# SPEC: Notion AI Orchestrator-Reasoner Architecture (v2 — Performance-First)

> Phiên bản này viết lại từ v1, ưu tiên **độ trễ thấp nhất / hiệu quả cao nhất**.
> Nguyên tắc xuyên suốt: tool chạy local (sub-ms) → **toàn bộ latency nằm ở số lần gọi model và số token mỗi lần gọi**. Mọi quyết định thiết kế đều xoay quanh việc *giảm số model round-trip* và *giảm token đẩy vào reasoner*.

---

## 1. Overview

### 1.1 Problem Statement
- Notion AI qua API (`notion.lgmmo.click`) **chỉ chat**, không có internal tools (filesystem, search, workspace access).
- Khi được yêu cầu đọc file nó chỉ trả text "I can't access the filesystem".
- Mục tiêu: "mượn" khả năng suy luận mạnh của Notion AI và ghép với một lớp tool execution local để khai thác hết tiềm năng.

### 1.2 Solution
Kiến trúc lai 2 model, nhưng **router local điều phối**, không để model tự quyết routing (tránh tốn round-trip):
- **Local Router + Tool Planner**: quyết intent và synthesize tool plan trực tiếp khi path/search/list rõ ràng — nhanh nhất, không tốn model RTT.
- **Executor (mimo-v2.5-pro)**: fallback planner/context gatherer khi router không đủ chắc — rẻ, nhanh, không phải dependency bắt buộc cho path rõ.
- **Reasoner (Notion AI / "opus-4.8")**: chỉ làm reasoning/planning/analysis trên context đã được chắt lọc — đắt, chậm, gọi tối đa 1 lần.

### 1.3 Goals
- [ ] Notion AI có thể "đọc" workspace/filesystem thông qua executor + tool layer.
- [ ] Hỗ trợ tool chạy local song song: `read_files`, `search`, `list_dir`; `edit_file` ở Alpha; `execute` hard-off cho đến security review.
- [ ] **Latency mục tiêu**: p50 < 3s, p99 < 12s; TTFT (time-to-first-token) < 2s nhờ streaming.
- [ ] Worst-case task phức tạp ≤ **2 model round-trip**.
- [ ] Tối ưu cost: mimo gom tool (rẻ), Notion chỉ reason (đắt) với token tối thiểu.
- [ ] OpenAI-compatible; tích hợp trước vào route hiện có `POST /v1/chat/completions` bằng opt-in (`model:auto` hoặc `agent_config.enabled=true`).

### 1.4 Non-Goals (v1)
- Multi-agent parallel reasoning.
- ML-based intent classifier (dùng rule + heuristic trước).
- Distributed cache / multi-node (in-memory trước).

---

## 2. Latency Model (kim chỉ nam thiết kế)

```
Total ≈ Σ(model round-trips) × (network_RTT + prefill(input_tokens) + output_tokens × per_token)
      + Σ(tool_exec)        // local, ~0
```

Hệ quả thiết kế:
1. **Giảm SỐ round-trip** → router quyết upfront, direct local tool plan khi đủ chắc, tool số nhiều, tool song song, straight-to-reasoner.
2. **Giảm TOKEN reasoner** → distill context, strip scaffolding, max_tokens chặt.
3. **Giảm overhead cố định** → connection pool, HTTP/2 keep-alive, co-locate.
4. **Giảm latency cảm nhận** → streaming TTFT.

---

## 3. Architecture

### 3.1 High-Level Architecture

```
┌───────────────────────────────────────────────────────────────────────┐
│                       API Gateway (api.lgmmo.click)                     │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │                        Orchestrator Layer                          │ │
│  │                                                                    │ │
│  │  ┌──────────────────────┐         ┌──────────────────────────────┐ │ │
│  │  │  Router (LOCAL ~0ms)  │         │      Context Manager         │ │ │
│  │  │  - intent + complexity│         │  - tool results (distilled)   │ │ │
│  │  │  - QUYẾT reasoning     │         │  - conversation history       │ │ │
│  │  │    upfront            │         │  - token budgeter             │ │ │
│  │  │  - chọn/synthesize tool│         │                              │ │ │
│  │  └──────────┬───────────┘         └───────────────┬──────────────┘ │ │
│  │             │                                     │                │ │
│  │  ┌──────────▼─────────────────────────────────────▼─────────────┐  │ │
│  │  │                  Agent Loop Controller                       │  │ │
│  │  │  fast-path: simple → 1 model, stream (1 RTT)                  │  │ │
│  │  │  direct-tool: local plan → tools∥ → [Reasoner/local reply]     │  │ │
│  │  │  - executor fallback only when router is unsure               │
│  │  │  - parallel tool exec (errgroup + semaphore)                 │  │ │
│  │  │  - straight-to-reasoner (KHÔNG loop về executor để finalize)  │  │ │
│  │  │  - streaming passthrough ra client                           │  │ │
│  │  └──────────┬─────────────────────────────────────┬─────────────┘  │ │
│  │             │                                     │                │ │
│  │  ┌──────────▼─────────┐               ┌───────────▼──────────────┐ │ │
│  │  │   Executor Pool     │               │      Reasoner Pool        │ │ │
│  │  │   mimo-v2.5-pro     │               │      Notion AI            │ │ │
│  │  │   (api.lgmmo)       │               │      (notion.lgmmo)       │ │ │
│  │  │   pooled HTTP/2     │               │      pooled HTTP/2         │ │ │
│  │  │   tools: gom context│               │      reason ≤ 1 lần        │ │ │
│  │  └─────────────────────┘               └───────────────────────────┘ │ │
│  │                                                                    │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  Tool Registry (local, sub-ms) + Cache + Prefetch                  │ │
│  │  - read_files (PLURAL, glob), list_dir(+preview), search(+range)   │ │
│  │  - edit_file, execute hard-off until security review                │ │
│  └───────────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────┘
```

### 3.2 Component Details

#### 3.2.1 Router (LOCAL — không gọi model)
Quyết định *upfront* để tránh tốn 1 RTT cho việc "model tự nhận cần reasoning". Khi request có path/search/list rõ, router synthesize `DirectToolPlan` và bỏ qua executor.

```go
type Router struct {
    classifier *IntentClassifier // rule + heuristic, chạy in-process
    routes     map[Intent]RouteConfig
}

type Decision struct {
    Intent        Intent
    Complexity    Complexity // low | medium | high
    NeedsTools    bool
    NeedsReasoner bool       // QUYẾT Ở ĐÂY, không để executor tự báo
    Tools         []string
    MaxIterations int
    Prefetch      *PrefetchHint // gợi ý read song song khi path rõ ràng
    DirectToolPlan []ToolCall   // tool calls do router synthesize, bỏ qua executor khi đủ chắc
}

type RouteConfig struct {
    ExecutorModel string
    ReasonerModel string
    Tools         []string
    MaxIterations int
    Timeout       time.Duration
}
```

#### 3.2.2 Agent Loop Controller (tối ưu round-trip)
```go
func (c *Controller) Run(ctx context.Context, req UserRequest, w StreamWriter) error {
    d := c.router.Decide(req) // LOCAL, ~µs

    if !d.NeedsTools {
        model := c.pick(d) // reasoner nếu cần suy luận, executor nếu chat thường
        return model.ChatStream(ctx, req.Messages, w) // 1 RTT
    }

    var prefetch <-chan ToolResult
    if d.Prefetch != nil {
        prefetch = c.tools.PrefetchAsync(ctx, d.Prefetch)
    }

    // DIRECT-TOOL-PATH: path/search/list rõ → không tốn executor RTT.
    if len(d.DirectToolPlan) > 0 {
        results := c.tools.RunParallel(ctx, d.DirectToolPlan, prefetch)
        if !d.NeedsReasoner {
            return c.localResponder.Stream(ctx, req, results, w) // 0 model RTT
        }
        distilled := c.ctxMgr.BuildReasonerContext(req, c.ctxMgr.Distill(results))
        return c.reasoner.ChatStream(ctx, distilled, w) // 1 RTT
    }

    // EXECUTOR-FALLBACK-PATH: chỉ dùng khi router không đủ chắc.
    messages := req.Messages
    for i := 0; i < d.MaxIterations; i++ {
        execResp := c.executor.Chat(ctx, messages, d.Tools) // 1 RTT
        if execResp.HasToolCalls() {
            results := c.tools.RunParallel(ctx, execResp.ToolCalls, prefetch)
            messages = append(messages, c.ctxMgr.Distill(results)...)
            if d.NeedsReasoner {
                distilled := c.ctxMgr.BuildReasonerContext(req, messages)
                return c.reasoner.ChatStream(ctx, distilled, w) // không quay lại executor finalize
            }
            return c.localResponder.Stream(ctx, req, results, w)
        }
        return execResp.StreamTo(w)
    }
    return ErrMaxIterations
}
```

Worst case task phức tạp khi router không đủ chắc: **Executor(gom) + Reasoner = 2 RTT**. Path rõ tốt hơn: local tools + Reasoner = **1 RTT**, hoặc tool-only = **0 RTT**.

#### 3.2.3 Tool Registry (thiết kế để giảm vòng lặp)
```go
var builtinTools = map[string]Tool{
    "read_files": &ReadFilesTool{}, // PLURAL + glob: 1 turn lấy N file
    "list_dir":   &ListDirTool{},   // kèm size/preview để khỏi read lại
    "search":     &SearchTool{},    // trả snippet + line range đủ context
    "edit_file":  &EditFileTool{},  // invalidate cache theo path
    "execute":    &ExecuteTool{},   // hard-off đến sau security review
}

type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage
    Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
```

Nguyên tắc thiết kế tool:
- **Số nhiều > số ít**: `read_files([...])` thay vì N lần `read_file`.
- **Giàu thông tin**: `list_dir` trả preview, `search` trả range → tránh follow-up read.
- **Hỗ trợ glob**: `read_files("**/*.go")` gom hết trong 1 turn.

#### 3.2.4 Context Manager (giảm token reasoner)
```go
type ContextManager struct {
    budget TokenBudget
}

// Distill: trích đúng đoạn liên quan (line range từ search), KHÔNG dump raw file.
func (m *ContextManager) Distill(results []ToolResult) []Message { ... }

// BuildReasonerContext: strip tool-call JSON/scaffolding, chỉ giữ
// user query + context đã chắt lọc, áp token budget.
func (m *ContextManager) BuildReasonerContext(req UserRequest, msgs []Message) []Message { ... }
```

---

## 4. Data Flow (đã tối ưu)

### 4.1 Simple Request (fast-path, 1 RTT)
```
User: "2+2?"  → Router: simple_chat, NeedsTools=false
  → 1 model, STREAM "4"   (TTFT < 1s)
```

### 4.2 Tool Request (direct local tool path)
```
User: "Đọc /tmp/test.txt"
  → Router: file_read, DirectToolPlan=read_files(/tmp/test.txt), NeedsReasoner=false
  → Tool read local → STREAM kết quả local
  → Tổng: 0 model RTT

User: "Đọc /tmp/test.txt và tóm tắt"
  → Router: file_read_summary, DirectToolPlan=read_files(/tmp/test.txt), NeedsReasoner=true
  → Tool read local → distilled context → Reasoner stream summary
  → Tổng: 1 model RTT
```

### 4.3 Complex Request (2 RTT, song song + straight-to-reasoner)
```
User: "Đọc tất cả file Go và đề xuất refactor"
  → Router: code_analysis, complexity=high, NeedsReasoner=true
Loop 1:
  Router direct plan: read_files("**/*.go") nếu path/glob rõ; nếu không rõ mới gọi Executor fallback
  Tools: chạy song song, trả về N file
  ContextManager: distill → chỉ giữ đoạn liên quan
  → straight-to-Reasoner (không quay lại executor)
Reasoner: STREAM đề xuất refactor ra client trực tiếp
  → Tổng: 1 model RTT nếu direct plan; tối đa 2 model RTT nếu cần Executor fallback
```

---

## 5. API Design (OpenAI-compatible + streaming)

### 5.1 Request

Prototype dùng route hiện có `POST /v1/chat/completions`. Orchestrator chỉ bật khi `model` là `auto` hoặc `agent_config.enabled=true`; mode thường giữ nguyên behavior hiện tại.
```json
{
  "model": "auto",
  "stream": true,
  "messages": [{"role": "user", "content": "Đọc /tmp/test.txt và tóm tắt"}],
  "tools": [
    {"type": "function", "function": {
      "name": "read_files",
      "description": "Read one or many files (supports glob)",
      "parameters": {"type": "object",
        "properties": {"paths": {"type": "array", "items": {"type": "string"}}},
        "required": ["paths"]}
    }}
  ],
  "agent_config": {
    "enabled": true,
    "executor_model": "mimo-v2.5-pro",
    "reasoner_model": "opus-4.8",
    "max_iterations": 5,
    "routing": "local",          // router quyết upfront
    "parallel_tools": true,
    "prefetch": true,
    "hedge_reasoner_ms": 800      // hedged request cho p99 (cost không phải mối lo)
  }
}
```

### 5.2 Response (streaming SSE; summary trace ở cuối)
```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "choices": [{"message": {"role": "assistant", "content": "..."}, "finish_reason": "stop"}],
  "usage": {"executor_tokens": 150, "reasoner_tokens": 300, "total_tokens": 450},
  "agent_trace": {
    "iterations": 1,
    "model_round_trips": 2,
    "tool_calls": 1,
    "reasoner_calls": 1,
    "prefetch_hit": true,
    "ttft_ms": 1450,
    "stage_latency_ms": {
      "router": 0, "executor_rtt": 1900, "tool_exec": 6,
      "context_distill": 3, "reasoner_rtt": 4200, "network": 120
    }
  }
}
```

---

## 6. Configuration

```json
{
  "orchestrator": {
    "enabled": true,
    "default_executor": "mimo-v2.5-pro",
    "default_reasoner": "opus-4.8",
    "max_iterations": 5,
    "timeout_sec": 120,
    "routing": "local",
    "parallel_tools": true,
    "prefetch": true,
    "stream": true,
    "hedge_reasoner_ms": 800
  },
  "models": {
    "executors": {
      "mimo-v2.5-pro": {
        "endpoint": "https://api.lgmmo.click/v1/chat/completions",
        "api_key": "Dyu123@as",
        "max_concurrent": 10,
        "timeout_sec": 30,
        "http2": true,
        "keep_alive": true,
        "max_idle_conns_per_host": 32
      }
    },
    "reasoners": {
      "opus-4.8": {
        "endpoint": "https://notion.lgmmo.click/v1/chat/completions",
        "api_key": "Dyu123@as",
        "max_concurrent": 5,
        "timeout_sec": 60,
        "http2": true,
        "keep_alive": true,
        "max_idle_conns_per_host": 16,
        "max_tokens": 1024
      }
    }
  },
  "tools": {
    "enabled": true,
    "allowed": ["read_files", "edit_file", "list_dir", "search"],
    "blocked": ["execute"],
    "sandbox": {
      "enabled": true,
      "allowed_paths": ["/tmp", "/home/user/projects"],
      "blocked_paths": ["/etc", "/var"]
    }
  },
  "cache": {
    "tool_result": {"enabled": true, "ttl_sec": 300, "key": "path+mtime+size"},
    "reasoner_response": {"enabled": true, "ttl_sec": 1800, "key": "hash(query+context)"},
    "prefix_cache": {"enabled": true, "note": "giữ system+tool schema cố định ở đầu prompt"}
  }
}
```

### 6.1 Intent Classification Rules (rule + heuristic, song ngữ)
```json
{
  "intents": {
    "file_read":     {"patterns": ["read","cat","show","view","xem","đọc"], "complexity":"medium", "tools":["read_files"], "needs_reasoner": false},
    "file_edit":     {"patterns": ["edit","modify","change","update","sửa"], "complexity":"medium", "tools":["read_files","edit_file"], "needs_reasoner": false},
    "code_analysis": {"patterns": ["analyze","review","refactor","suggest","phân tích","hiểu","giải thích"], "complexity":"high", "tools":["list_dir","read_files","search"], "needs_reasoner": true},
    "simple_chat":   {"patterns": ["hello","hi","xin chào"], "complexity":"low", "tools":[], "needs_reasoner": false}
  },
  "fallback": {"complexity":"medium", "tools":["read_files","search"], "needs_reasoner": true,
    "note": "khi không khớp keyword, ưu tiên an toàn: cho tool + reasoner"}
}
```

---

## 7. Performance Optimizations (chi tiết)

### 7.1 Giảm số model round-trip (impact #1)
- **Direct local tool plan khi đủ chắc** → path rõ không cần executor RTT.
- **Router quyết reasoning upfront** → cắt 1 RTT "executor tự nhận".
- **`read_files` số nhiều + glob** → N→1 RTT.
- **Tool song song** (`errgroup` + bounded semaphore).
- **Straight-to-reasoner**: sau khi gom context không quay lại executor.
- **Fast-path** cho simple_chat → 1 RTT.

### 7.2 Giảm token reasoner (impact #2)
- **Distill context**: trích line range từ `search`, không dump raw file.
- **Strip scaffolding** tool-call JSON khỏi message gửi reasoner.
- **max_tokens chặt** + gọi reasoner ≤ 1 lần/request.

### 7.3 Streaming (perceived latency)
- SSE passthrough từ reasoner → client; nếu upstream không stream thật, gửi keepalive/header sớm và đo riêng `header_ttft_ms` với `content_ttft_ms`.
- Parse incremental executor stream để bắt tool_call sớm, kick tool ngay.

### 7.4 Network/concurrency
- Reuse `*http.Client`, HTTP/2 + keep-alive, `MaxIdleConnsPerHost` cao.
- Co-locate orchestrator gần endpoint để giảm RTT mạng.
- gzip payload lớn; propagate `context` cancel để hủy upstream khi client ngắt.

### 7.5 Caching
- Tool cache key `(path, mtime, size)`, invalidate khi `edit_file`.
- Prefix cache: giữ system + tool schema cố định đầu prompt (hit prefill cache).
- Reasoner response cache theo `hash(query+distilled_context)`.

### 7.6 Nâng cao
- **Prefetch**: router đoán path rõ → đọc song song với executor call.
- **Hedged request** cho reasoner: gửi bản 2 sau `hedge_reasoner_ms`, lấy bản về trước (đánh đổi cost lấy p99 — chấp nhận được).

---

## 8. Risk Mitigation

### 8.1 Technical
| Risk | Impact | Mitigation |
|------|--------|------------|
| Reasoner (Notion) chậm/đổi format | High | Hedged request, timeout, response cache, health check, fallback executor-only |
| Executor timeout | High | Circuit breaker, retry backoff |
| Tool execution error | Medium | Retry, graceful degradation, error vào context cho model tự xử lý |
| Infinite loop | High | max_iterations + timeout + context cancel |
| Router misroute (keyword giòn) | Medium | Fallback an toàn (cho tool+reasoner), log để cải thiện rule |

### 8.2 Operational
| Risk | Impact | Mitigation |
|------|--------|------------|
| Debug khó (2 model) | Medium | Trace ID + per-stage latency span |
| Latency tail (p99) | Medium | Hedging, prefetch, cache |
| Token waste | Low | Distill + fast-path skip reasoner |

---

## 9. Testing Strategy
- **Unit**: router/classifier, controller loop, parallel tool exec, context distill, cache.
- **Integration**: end-to-end fast-path / tool-path / complex; fallback; streaming.
- **Load**: concurrent requests, đo `model_round_trips`, TTFT, p50/p99 từng stage.
- **Latency regression gate**: CI fail nếu p99 hoặc số round-trip vượt ngưỡng.

---

## 10. Open Questions
1. mimo-v2.5-pro có emit **multi tool-call trong 1 lượt** ổn định không? (không còn là blocker vì direct local planner xử lý path rõ)
2. Notion endpoint có hỗ trợ **streaming SSE** không? Nếu không, TTFT win sẽ giảm — cần buffer chiến lược.
3. mimo có hỗ trợ **prefix/KV cache** để hit prefill không?
4. Ngưỡng `hedge_reasoner_ms` tối ưu là bao nhiêu (đo thực tế)?
5. Sandbox scope cho `execute` (giữ default-off ở v1).

---

## 11. Decision Points (đã chốt cho v2)
- [x] **API integration**: dùng route hiện có `POST /v1/chat/completions` với opt-in, không tạo route mới ở prototype.
- [x] **Routing**: LOCAL upfront (không để model tự quyết) — vì latency.
- [x] **Tool planning**: direct local tool plan khi router đủ chắc; executor chỉ fallback.
- [x] **Tool exec**: song song (errgroup).
- [x] **Reasoner**: gọi ≤ 1 lần, straight-to-reasoner, streaming.
- [x] **Tool granularity**: số nhiều (`read_files`) + glob.
- [x] Ngôn ngữ: Go. Queue: in-memory (v1). Storage: SQLite (v1).
- [ ] ML classifier: hoãn (rule + fallback đủ cho v1).
