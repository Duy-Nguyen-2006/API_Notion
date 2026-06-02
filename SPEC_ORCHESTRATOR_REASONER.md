# SPEC: Notion AI Orchestrator-Reasoner Architecture

## 1. Overview

### 1.1 Problem Statement
- Notion AI qua API không có internal tools (filesystem, search, workspace access)
- Chỉ trả text "I can't access the filesystem" khi được yêu cầu đọc files
- Không thể exploit full potential của Notion AI như khi dùng trên web

### 1.2 Solution
Hybrid architecture với 2 models:
- **Executor (mimo-v2.5-pro)**: Xử lý tool calls, file operations, nhanh, rẻ
- **Reasoner (Notion AI - opus-4.8)**: Xử lý reasoning, planning, thông minh

### 1.3 Goals
- [ ] Notion AI có thể "đọc" workspace thông qua executor
- [ ] Hỗ trợ tool calls: read_file, edit_file, search, list_dir
- [ ] Tối ưu cost: mimo xử lý tools (rẻ), Notion xử lý reasoning (đắt)
- [ ] Future-proof: Merge vào api.lgmmo.click sau

## 2. Architecture

### 2.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     API Gateway (api.lgmmo.click)           │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │                 Orchestrator Layer                    │  │
│  │                                                      │  │
│  │  ┌─────────────────┐      ┌─────────────────────┐  │  │
│  │  │ Request Router   │      │ Context Manager     │  │  │
│  │  │ - Parse intent   │      │ - Tool results      │  │  │
│  │  │ - Route to agent │      │ - Conversation hist  │  │  │
│  │  └────────┬─────────┘      └──────────┬──────────┘  │  │
│  │           │                           │              │  │
│  │  ┌────────▼───────────────────────────▼──────────┐  │  │
│  │  │              Agent Loop Controller             │  │  │
│  │  │  1. Send to Executor (mimo)                    │  │  │
│  │  │  2. If tool_call → execute → loop              │  │  │
│  │  │  3. If reasoning_needed → send to Reasoner     │  │  │
│  │  │  4. Return final response                      │  │  │
│  │  └────────┬───────────────────────────┬──────────┘  │  │
│  │           │                           │              │  │
│  │  ┌────────▼─────────┐      ┌─────────▼──────────┐  │  │
│  │  │  Executor Pool   │      │   Reasoner Pool    │  │  │
│  │  │  mimo-v2.5-pro   │      │   Notion AI        │  │  │
│  │  │  (api.lgmmo)     │      │   (notion.lgmmo)   │  │  │
│  │  │                  │      │                    │  │  │
│  │  │  Tools:          │      │  Capabilities:     │  │  │
│  │  │  - read_file     │      │  - Reasoning       │  │  │
│  │  │  - edit_file     │      │  - Planning        │  │  │
│  │  │  - search        │      │  - Summarization   │  │  │
│  │  │  - list_dir      │      │  - Analysis        │  │  │
│  │  │  - execute_cmd   │      │  - Code review     │  │  │
│  │  └──────────────────┘      └────────────────────┘  │  │
│  │                                                      │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │                   Tool Registry                       │  │
│  │  - File system tools                                 │  │
│  │  - Notion workspace tools (future)                   │  │
│  │  - External API tools (future)                       │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Component Details

#### 2.2.1 Request Router
```go
type RequestRouter struct {
    intentClassifier *IntentClassifier
    routeTable       map[string]RouteConfig
}

type RouteConfig struct {
    ExecutorModel  string   // "mimo-v2.5-pro"
    ReasonerModel  string   // "opus-4.8"
    Tools          []string // Available tools for this intent
    MaxIterations  int      // Max agent loop iterations
    Timeout        time.Duration
}
```

#### 2.2.2 Agent Loop Controller
```go
type AgentLoopController struct {
    executor  ModelClient
    reasoner  ModelClient
    toolExec  ToolExecutor
    maxIter   int
}

func (c *AgentLoopController) Run(ctx context.Context, req UserRequest) Response {
    messages := []Message{req.ToMessage()}
    
    for i := 0; i < c.maxIter; i++ {
        // Step 1: Send to executor (mimo)
        execResp := c.executor.Chat(ctx, messages)
        
        if execResp.HasToolCalls() {
            // Step 2: Execute tools
            for _, tc := range execResp.ToolCalls {
                result := c.toolExec.Execute(tc)
                messages = append(messages, ToolResultMessage(tc.ID, result))
            }
            continue // Loop back to executor
        }
        
        if execResp.NeedsReasoning() {
            // Step 3: Send to reasoner (Notion AI)
            reasonerResp := c.reasoner.Chat(ctx, messages)
            return reasonerResp.ToFinalResponse()
        }
        
        // Step 4: Return final response
        return execResp.ToFinalResponse()
    }
    
    return Error("Max iterations exceeded")
}
```

#### 2.2.3 Tool Registry
```go
type ToolRegistry struct {
    tools map[string]Tool
}

type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage
    Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Built-in tools
var builtinTools = map[string]Tool{
    "read_file":  &ReadFileTool{},
    "edit_file":  &EditFileTool{},
    "list_dir":   &ListDirTool{},
    "search":     &SearchTool{},
    "execute":    &ExecuteTool{},
}
```

## 3. Data Flow

### 3.1 Simple Request (No Tools)
```
User: "What is 2+2?"
  ↓
Router: intent=math, complexity=simple
  ↓
Executor (mimo): "4"
  ↓
Response: "4"
```

### 3.2 Tool Request (Read File)
```
User: "Read /tmp/test.txt"
  ↓
Router: intent=file_read, complexity=medium
  ↓
Executor (mimo): tool_call{name: "read_file", args: {path: "/tmp/test.txt"}}
  ↓
ToolExecutor: execute read_file → "Hello world..."
  ↓
Executor (mimo): "The file contains: Hello world..."
  ↓
Response: "The file contains: Hello world..."
```

### 3.3 Complex Request (Need Reasoning)
```
User: "Read all Go files in this project and suggest refactoring"
  ↓
Router: intent=code_analysis, complexity=high
  ↓
Loop 1:
  Executor (mimo): tool_call{name: "list_dir", args: {path: "."}}
  ToolExecutor: returns [main.go, utils.go, ...]
  
Loop 2:
  Executor (mimo): tool_call{name: "read_file", args: {path: "main.go"}}
  ToolExecutor: returns file content
  
Loop 3:
  Executor (mimo): tool_call{name: "read_file", args: {path: "utils.go"}}
  ToolExecutor: returns file content
  
Loop 4:
  Executor (mimo): needs_reasoning=true
  ↓
Reasoner (Notion AI): Analyzes code, suggests refactoring
  ↓
Response: Detailed refactoring suggestions
```

## 4. API Design

### 4.1 Request Format
```json
{
  "model": "auto",
  "messages": [
    {"role": "user", "content": "Read /tmp/test.txt and summarize"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "read_file",
        "description": "Read a file",
        "parameters": {
          "type": "object",
          "properties": {
            "path": {"type": "string"}
          },
          "required": ["path"]
        }
      }
    }
  ],
  "agent_config": {
    "executor_model": "mimo-v2.5-pro",
    "reasoner_model": "opus-4.8",
    "max_iterations": 5,
    "reasoning_threshold": 0.7
  }
}
```

### 4.2 Response Format
```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "The file contains a greeting message..."
    },
    "finish_reason": "stop"
  }],
  "usage": {
    "executor_tokens": 150,
    "reasoner_tokens": 300,
    "total_tokens": 450
  },
  "agent_trace": {
    "iterations": 2,
    "tool_calls": 1,
    "reasoner_calls": 0,
    "executor_model": "mimo-v2.5-pro",
    "reasoner_model": "opus-4.8"
  }
}
```

## 5. Configuration

### 5.1 Config Schema
```json
{
  "orchestrator": {
    "enabled": true,
    "default_executor": "mimo-v2.5-pro",
    "default_reasoner": "opus-4.8",
    "max_iterations": 5,
    "timeout_sec": 120,
    "reasoning_threshold": 0.7
  },
  "models": {
    "executors": {
      "mimo-v2.5-pro": {
        "endpoint": "https://api.lgmmo.click/v1/chat/completions",
        "api_key": "Dyu123@as",
        "max_concurrent": 10,
        "timeout_sec": 30
      }
    },
    "reasoners": {
      "opus-4.8": {
        "endpoint": "https://notion.lgmmo.click/v1/chat/completions",
        "api_key": "Dyu123@as",
        "max_concurrent": 5,
        "timeout_sec": 60
      }
    }
  },
  "tools": {
    "enabled": true,
    "allowed": ["read_file", "edit_file", "list_dir", "search"],
    "blocked": ["execute_command"],
    "sandbox": {
      "enabled": true,
      "allowed_paths": ["/tmp", "/home/user/projects"],
      "blocked_paths": ["/etc", "/var"]
    }
  }
}
```

### 5.2 Intent Classification Rules
```json
{
  "intents": {
    "file_read": {
      "patterns": ["read", "cat", "show", "view", "xem", "đọc"],
      "complexity": "medium",
      "tools": ["read_file"]
    },
    "file_edit": {
      "patterns": ["edit", "modify", "change", "update", "sửa"],
      "complexity": "medium",
      "tools": ["read_file", "edit_file"]
    },
    "code_analysis": {
      "patterns": ["analyze", "review", "refactor", "suggest", "phân tích"],
      "complexity": "high",
      "tools": ["list_dir", "read_file", "search"],
      "needs_reasoning": true
    },
    "simple_chat": {
      "patterns": ["hello", "hi", "xin chào"],
      "complexity": "low",
      "tools": []
    }
  }
}
```

## 6. Risk Mitigation

### 6.1 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Executor timeout | High | Circuit breaker, fallback to direct Notion |
| Reasoner timeout | High | Cache common responses, timeout handling |
| Tool execution error | Medium | Retry with backoff, graceful degradation |
| Infinite loop | High | Max iterations limit, timeout |
| Cost overrun | Medium | Token budget, rate limiting |
| Model unavailable | High | Fallback models, health checks |

### 6.2 Operational Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Debug difficulty | Medium | Detailed logging, trace ID |
| Latency increase | Medium | Async tool execution, caching |
| Token waste | Medium | Smart routing, skip unnecessary reasoning |

## 7. Testing Strategy

### 7.1 Unit Tests
- Intent classifier
- Agent loop controller
- Tool executor
- Request router

### 7.2 Integration Tests
- End-to-end flow
- Tool execution
- Model fallback
- Error handling

### 7.3 Load Tests
- Concurrent requests
- Token consumption
- Latency benchmarks

## 8. Migration Path

### Phase 1: Prototype (Week 1-2)
- [ ] Basic agent loop
- [ ] 3 tools: read_file, list_dir, search
- [ ] Single executor/reasoner pair
- [ ] Unit tests

### Phase 2: Alpha (Week 3-4)
- [ ] All file tools
- [ ] Intent classifier
- [ ] Error handling
- [ ] Integration tests

### Phase 3: Beta (Week 5-6)
- [ ] Performance optimization
- [ ] Load testing
- [ ] Documentation
- [ ] API stability

### Phase 4: Production (Week 7-8)
- [ ] Merge into api.lgmmo.click
- [ ] Monitoring
- [ ] Gradual rollout
- [ ] Feedback collection

## 9. Open Questions

1. **Model Selection**: mimo-v2.5-pro đủ mạnh cho tool calls không? Hay cần model khác?
2. **Cost**: Chi phí ước tính cho 1000 requests/ngày?
3. **Latency**: Chấp nhận thêm bao nhiêu latency? (hiện tại: ~2s, target: <5s)
4. **Security**: Sandbox scope như thế nào? User isolation?
5. **Future**: Có cần support multiple executors/reasoners không?

## 10. Decision Points

### 10.1 Architecture Decisions
- [ ] Dùng single agent loop hay multi-agent?
- [ ] Intent classifier rule-based hay ML-based?
- [ ] Tool execution sync hay async?
- [ ] Cache strategy cho tool results?

### 10.2 Technology Decisions
- [ ] Ngôn ngữ: Go (hiện tại) hay Python (AI ecosystem)?
- [ ] Queue: In-memory hay Redis/RabbitMQ?
- [ ] Storage: SQLite (hiện tại) hay PostgreSQL?

### 10.3 API Decisions
- [ ] Backward compatible với OpenAI format?
- [ ] Thêm field mới hay tạo endpoint mới?
- [ ] Versioning strategy?
