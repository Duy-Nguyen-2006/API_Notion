# Security Review: Orchestrator-Reasoner Tool Execution

> Status: **Draft** — v1 prototype security assessment.
> Last updated: 2026-06-03
> Reviewer: [pending]

---

## 1. Scope

This document covers the security posture of the orchestrator tool execution layer (`internal/orchestrator/tools.go`) and its integration point (`internal/app/orchestrator_chat.go`). The analysis focuses on:

- Filesystem tool execution (`read_files`, `list_dir`, `search`, `edit_file`)
- The `execute` tool (hard-off in v1)
- Path traversal and symlink attacks
- Command injection vectors
- Denial-of-service via resource exhaustion

---

## 2. STRIDE Analysis

### 2.1 Spoofing

| Threat | Risk | Mitigation |
|--------|------|------------|
| Unauthenticated caller invokes orchestrator with `model: "auto"` | Medium | Orchestrator inherits auth from `/v1/chat/completions` handler — same API key required. No separate auth bypass. |
| Attacker crafts `agent_config.enabled: true` on non-orchestrator requests | Low | Auth gate is upstream; same credential check applies. |

### 2.2 Tampering

| Threat | Risk | Mitigation |
|--------|------|------------|
| Path traversal via `read_files(["../../../etc/passwd"])` | **High** | `safeJoin()` resolves paths and rejects escapes: `if resolved != base && !strings.HasPrefix(resolved, base+"/") { return error }`. See §3.1. |
| `edit_file` overwrites arbitrary files | **High** | Same `safeJoin()` constraint. Writes are bounded to root dir. |
| Symlink attack: symlink inside root points to `/etc/shadow` | **High** | `safeJoin` uses `filepath.Clean` + prefix check — does NOT follow symlinks before the check. See §3.2. |
| Glob expansion reads outside root | Medium | `expandPaths` calls `safeJoin` on the glob pattern before `filepath.Glob`. Pattern like `../../**` is rejected. |

### 2.3 Repudiation

| Threat | Risk | Mitigation |
|--------|------|------------|
| Tool calls not logged | Medium | `AgentTrace` records `tool_calls` count. Stage latency is recorded. Recommend: add structured audit log for each tool invocation (file, args hash, result). |
| Edit_file changes not tracked | Medium | Tool returns metadata (`path`, `replacements`). Recommend: audit log + git diff integration. |

### 2.4 Information Disclosure

| Threat | Risk | Mitigation |
|--------|------|------------|
| `read_files` reads sensitive files within root | Medium | No content-level filtering. Root dir scoping is the primary gate. Recommend: `blocked_paths` config for sensitive dirs (e.g., `.env`, `.git/credentials`). |
| Error messages leak internal paths | Low | Errors include resolved path (e.g., `/home/user/project/file.txt`). Acceptable for local API; consider stripping for public-facing deployments. |
| Search returns sensitive content snippets | Medium | `search` caps at 100 matches. No content filtering. Recommend: same `blocked_paths` config. |

### 2.5 Denial of Service

| Threat | Risk | Mitigation |
|--------|------|------------|
| `read_files` on huge files / many files | Medium | Semaphore bounds concurrency (default 8). No per-file size limit. Recommend: add `max_file_size_bytes` config. |
| `search` on deep directory tree | Medium | Caps at 100 matches. No depth limit. Recommend: add `max_search_depth` config. |
| Infinite orchestrator loop | High | `max_iterations` (default 1–2 from router) + context timeout. Already mitigated. |
| Rate limit exhaustion | Medium | `RateLimiter` + `Semaphore` in `Registry`. Configurable per-request. |

### 2.6 Elevation of Privilege

| Threat | Risk | Mitigation |
|--------|------|------------|
| `execute` tool runs arbitrary commands | **Critical** | **Hard-off**: `ExecuteTool.Execute()` always returns `errors.New("execute is hard-off pending security review")`. The `enabled` field is accepted but ignored — the rejection is unconditional. |
| `edit_file` used to modify `go.mod` or config files | Medium | Root scoping applies. No file-type filtering. Recommend: `blocked_patterns` config (e.g., `*.mod`, `config*.json`). |

---

## 3. Detailed Analysis

### 3.1 Path Traversal — `safeJoin`

```go
func safeJoin(root string, path string) (string, error) {
    if filepath.IsAbs(path) {
        return filepath.Clean(path), nil  // ⚠ See note below
    }
    base, err := filepath.Abs(root)
    resolved := filepath.Clean(filepath.Join(base, path))
    if resolved != base && !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
        return "", fmt.Errorf("path escapes root: %s", path)
    }
    return resolved, nil
}
```

**Finding**: Absolute paths (`filepath.IsAbs`) bypass the root check entirely. If `path` is `/etc/passwd`, it returns `/etc/passwd` directly.

**Severity**: Medium — The router's `DirectToolPlan` typically passes relative paths extracted from user prompts. However, a crafted request with an absolute path in `args` would bypass root scoping.

**Recommendation**: Reject absolute paths or always apply `safeJoin` prefix check:
```go
if filepath.IsAbs(path) {
    resolved := filepath.Clean(path)
    base, _ := filepath.Abs(root)
    if !strings.HasPrefix(resolved, base+string(os.PathSeparator)) && resolved != base {
        return "", fmt.Errorf("absolute path outside root: %s", path)
    }
    return resolved, nil
}
```

### 3.2 Symlink Attacks

`safeJoin` uses `filepath.Clean` + prefix check on the **requested path**, not the resolved symlink target. If `root/link -> /etc`, then `safeJoin(root, "link/passwd")` would return `root/link/passwd` which the OS would resolve to `/etc/passwd`.

**Severity**: Medium — The check passes because `root/link/passwd` has the `root/` prefix, but the actual file read is outside root.

**Mitigation**: Use `filepath.EvalSymlinks` after `safeJoin` and verify the resolved path is still under root:
```go
resolved, err := filepath.EvalSymlinks(path)
if err == nil && !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
    return "", fmt.Errorf("symlink escapes root: %s", path)
}
```

### 3.3 `execute` Tool — Hard-Off Analysis

```go
func (t *ExecuteTool) Execute(ctx context.Context, args map[string]any) (ToolResult, error) {
    return ToolResult{
        Content:  `{"error":"execute_disabled","message":"execute is hard-off pending security review"}`,
        Metadata: map[string]any{"rejected": true, "reason": "execute_disabled"},
    }, errors.New("execute is hard-off pending security review")
}
```

**Status**: The `ExecuteTool` unconditionally rejects all commands. The `enabled` field in `ExecuteTool` struct is accepted but the `Execute` method does not check it — the rejection is hardcoded. This is the correct security posture for v1.

**Future consideration** (when/if `execute` is enabled):
- Require sandbox (e.g., `firejail`, `bubblewrap`, or container)
- Allowlist commands (not denylist)
- No shell interpretation (pass args array, not string)
- Timeout + resource limits (cgroup)
- Network namespace isolation
- Read-only filesystem where possible
- Seccomp profile

---

## 4. Mitigations Already in Place

| Mitigation | Location | Effectiveness |
|-----------|----------|--------------|
| `safeJoin` root containment | `tools.go` | High (relative paths). Medium (absolute paths — gap found). |
| `Execute` hard-off | `tools.go` | Critical — unconditional rejection. |
| Tool concurrency semaphore | `tools.go` | High — bounds parallel execution. |
| Rate limiter | `tools.go` | High — configurable per-second limit. |
| `max_iterations` | `loop.go` | High — prevents infinite loops. |
| Context cancellation | `loop.go` | High — propagates timeout/cancel. |
| Response cache TTL | `cache.go` | Medium — prevents stale data, bounds memory. |
| Tool cache by (path, mtime, size) | `cache.go` | High — correct invalidation on edit. |

---

## 5. Recommendations for `execute` Sandbox (When Enabled)

If/when the `execute` tool is enabled, implement the following layered defense:

### 5.1 Command Allowlist
```json
{
  "execute": {
    "allowed_commands": ["go", "python3", "node", "ls", "cat", "grep", "find"],
    "blocked_args": ["-exec", "-o", "|", "&&", ";", "`", "$("]
  }
}
```

### 5.2 Sandbox Requirements
- **Namespace isolation**: mount, PID, network, user namespaces
- **Seccomp**: Whitelist syscalls (read, write, open, stat, mmap, brk, etc.)
- **Resource limits**: cgroup v2 (CPU 50%, memory 256MB, no swap)
- **Timeout**: Hard kill after 30s
- **No shell**: Use `exec.Command(cmd, args...)` — never `sh -c`
- **Working directory**: Set to root dir, disallow `cd` outside

### 5.3 Audit Logging
- Log every execute call: command, args, user, timestamp, exit code, stdout/stderr hash
- Retain logs for 90 days
- Alert on blocked command attempts

---

## 6. Sign-Off Checklist

| # | Item | Status | Notes |
|---|------|--------|-------|
| 1 | `execute` tool is hard-off (unconditional rejection) | ✅ PASS | Verified in code and tests |
| 2 | `safeJoin` prevents relative path traversal | ✅ PASS | Prefix check on resolved path |
| 3 | `safeJoin` handles absolute paths | ⚠️ GAP | Absolute paths bypass root check |
| 4 | Symlink traversal prevention | ⚠️ GAP | No `EvalSymlinks` check |
| 5 | `edit_file` scoped to root | ✅ PASS | Uses `safeJoin` |
| 6 | Glob expansion bounded to root | ✅ PASS | Pattern goes through `safeJoin` |
| 7 | Tool concurrency bounded | ✅ PASS | Semaphore default 8 |
| 8 | Rate limiting available | ✅ PASS | Configurable per-second |
| 9 | Max iterations enforced | ✅ PASS | Router sets 1–2, configurable |
| 10 | Context cancellation propagated | ✅ PASS | All tools check `ctx.Done()` |
| 11 | Response cache TTL | ✅ PASS | 30min default, configurable |
| 12 | No sensitive data in logs | ⚠️ REVIEW | Tool results may contain file contents |
| 13 | `edit_file` blocks sensitive files | ❌ NOT IMPLEMENTED | No blocked_patterns config |
| 14 | Per-file size limit | ❌ NOT IMPLEMENTED | No max_file_size_bytes |
| 15 | Search depth limit | ❌ NOT IMPLEMENTED | No max_search_depth |

### Blocking Issues (must resolve before production)
1. **Absolute path bypass in `safeJoin`** — Reject or validate absolute paths against root.
2. **Symlink traversal** — Add `filepath.EvalSymlinks` check.

### Non-Blocking Recommendations (can address in follow-up)
3. Add `blocked_paths` / `blocked_patterns` config for sensitive files.
4. Add `max_file_size_bytes` limit.
5. Add structured audit logging for tool invocations.
6. Add `max_search_depth` for the search tool.
7. Strip internal paths from error messages in public-facing deployments.

---

*This document should be reviewed and signed off before Phase 4 (Production) gate.*
