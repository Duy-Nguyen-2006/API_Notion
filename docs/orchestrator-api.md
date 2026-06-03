# Orchestrator API Reference

> Last updated: 2026-06-03

The orchestrator is an opt-in layer on top of the existing `POST /v1/chat/completions` endpoint. It adds local tool execution, multi-model routing, and agent tracing.

---

## 1. Enabling the Orchestrator

The orchestrator activates when **either** condition is met:

1. `model` is set to `"auto"`
2. `agent_config.enabled` is `true`

If neither is set, the request follows the standard chat completion path.

---

## 2. Request Format

### 2.1 Minimal Request

```json
{
  "model": "auto",
  "messages": [
    {"role": "user", "content": "read README.md"}
  ]
}
```

### 2.2 Full Request with Agent Config

```json
{
  "model": "auto",
  "stream": true,
  "messages": [
    {"role": "user", "content": "Analyze this project and suggest refactors"}
  ],
  "agent_config": {
    "enabled": true,
    "executor_model": "mimo-v2.5-pro",
    "reasoner_model": "opus-4.8",
    "max_iterations": 5,
    "routing": "local",
    "parallel_tools": true,
    "prefetch": true,
    "hedge_reasoner_ms": 800
  }
}
```

### 2.3 Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `model` | string | Yes | Set to `"auto"` to enable orchestrator. Any other value uses the standard path. |
| `messages` | array | Yes | OpenAI-compatible message array. |
| `stream` | boolean | No | Enable SSE streaming (default: `false`). |
| `agent_config.enabled` | boolean | No | Alternative to `model: "auto"` to enable orchestrator. |
| `agent_config.executor_model` | string | No | Model for executor role (default: configured default). |
| `agent_config.reasoner_model` | string | No | Model for reasoner role (default: configured default). |
| `agent_config.max_iterations` | int | No | Max tool execution loops (default: 1–2 from router). |
| `agent_config.hedge_reasoner_ms` | int | No | Delay before hedged reasoner request in ms (default: 800, 0 to disable). |

---

## 3. Response Format

### 3.1 Non-Streaming Response

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1717401600,
  "model": "auto",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "The README describes..."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 150,
    "completion_tokens": 300,
    "total_tokens": 450
  },
  "agent_trace": {
    "trace_id": "abc-123",
    "iterations": 1,
    "model_round_trips": 1,
    "tool_calls": 1,
    "reasoner_calls": 1,
    "executor_calls": 0,
    "prefetch_hit": false,
    "ttft_ms": 1450,
    "stage_latency_ms": {
      "router": 0,
      "tool_exec": 6,
      "context_distill": 3,
      "reasoner_rtt": 4200
    }
  }
}
```

### 3.2 Agent Trace Fields

| Field | Type | Description |
|-------|------|-------------|
| `trace_id` | string | Unique trace identifier. |
| `iterations` | int | Number of loop iterations executed. |
| `model_round_trips` | int | Total model API calls (executor + reasoner + hedges). |
| `tool_calls` | int | Number of tool invocations. |
| `reasoner_calls` | int | Reasoner API calls (may be 2 if hedged). |
| `executor_calls` | int | Executor API calls. |
| `prefetch_hit` | boolean | Whether a prefetched tool result was used. |
| `ttft_ms` | int | Time-to-first-token in milliseconds (end-to-end). |
| `stage_latency_ms` | object | Per-stage latency breakdown in ms. |

### 3.3 Stage Latency Keys

| Key | Description |
|-----|-------------|
| `router` | Local router decision time (~0ms). |
| `tool_exec` | Tool execution time (local, typically < 10ms). |
| `context_distill` | Context distillation time. |
| `executor_rtt` | Executor model round-trip time. |
| `reasoner_rtt` | Reasoner model round-trip time. |
| `reasoner_cache` | Cache hit (0ms when cached). |
| `reasoner_hedge` | Hedged request delay (if triggered). |

---

## 4. Streaming Behavior

When `stream: true`, the response is delivered as Server-Sent Events (SSE):

```
data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant"}}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"delta":{"content":"The "}}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"delta":{"content":"README "}}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"delta":{"content":"describes..."}}]}

data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"agent_trace":{"iterations":1,"model_round_trips":1,"tool_calls":1,"reasoner_calls":1,"ttft_ms":1450,"stage_latency_ms":{"router":0,"tool_exec":6,"reasoner_rtt":4200}}}

data: [DONE]
```

Key behaviors:
- **First event**: Contains `{"delta":{"role":"assistant"}}`.
- **Content events**: Each contains a `delta.content` chunk.
- **Finish event**: Contains `finish_reason: "stop"` and optional `usage`.
- **Agent trace event**: Emitted after the finish event as a separate SSE data frame.
- **Done**: `data: [DONE]` terminates the stream.

The agent trace is always emitted as the **last data event** before `[DONE]`, even in streaming mode.

---

## 5. Routing Behavior

The local router classifies requests into intents and decides the execution path:

| Intent | Tools | Model RTTs | Example |
|--------|-------|-----------|---------|
| `simple_chat` | None | 1 (reasoner) | "What is Go?" |
| `file_read` | `read_files` | 0 | "read main.go" |
| `file_read_summary` | `read_files` | 1 (reasoner) | "read main.go and summarize" |
| `file_edit` | `edit_file` | 0 | "edit main.go to fix the bug" |
| `list_dir` | `list_dir` | 0 | "list internal/" |
| `search` | `search` | 0–1 | "search for TODO" |
| `code_analysis` | `read_files`, `search`, `list_dir` | 1–2 | "analyze this project" |

### Path Extraction

The router extracts file paths from the prompt using regex patterns:
- Relative paths: `./path`, `../path`, `dir/file`
- File names with extensions: `main.go`, `README.md`
- Glob patterns: `**/*.go`, `internal/*.go`

### Fallback

If no intent matches, the request defaults to `simple_chat` with the reasoner.

---

## 6. Configuration Reference

### 6.1 Config File (`config.json`)

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
  }
}
```

### 6.2 Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `LOAD_CONCURRENCY` | Concurrency for load tests | 100 |
| `LOAD_DURATION` | Duration for load tests | 30s |

### 6.3 OrchestratorOptions (Go)

```go
type OrchestratorOptions struct {
    MaxConcurrentTools  int           // Default: 8
    RateLimitPerSecond  int           // Default: 0 (unlimited)
    HedgeReasonerAfter  time.Duration // Default: 800ms
    ExecuteEnabled      bool          // Default: false (hard-off)
    ToolCacheEnabled    bool          // Default: true
}
```

---

## 7. Error Handling

### 7.1 Orchestrator Errors

| Error | Cause | HTTP Status |
|-------|-------|-------------|
| `"execute is hard-off pending security review"` | Execute tool invoked | 200 (error in agent_trace) |
| `"executor client is required for fallback path"` | Complex request but no executor configured | 500 |
| `"reasoner client is required"` | Reasoner not configured | 500 |
| `"path escapes root: ..."` | Path traversal attempt | 200 (tool error) |
| `"tool not found"` | Unknown tool name | 200 (tool error) |
| `"old_string not found"` | Edit target string not in file | 200 (tool error) |

### 7.2 Error Response Format

Non-streaming:
```json
{
  "error": {
    "message": "executor client is required for fallback path",
    "type": "api_error",
    "code": "orchestrator_error"
  }
}
```

Streaming (SSE):
```
data: {"error":{"message":"...","type":"api_error","code":"orchestrator_error"}}
data: [DONE]
```

---

## 8. Tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `read_files` | Read one or more files (supports glob) | `paths: string[]` (required) |
| `list_dir` | List directory with file size preview | `path: string` (required) |
| `search` | Search files and return snippets with line numbers | `query: string` (required), `paths: string[]` (optional) |
| `edit_file` | Replace exact string in file, invalidate cache | `path: string`, `old_string: string`, `new_string: string` (all required) |
| `execute` | Command execution (HARD-OFF in v1) | `command: string` — always returns error |
