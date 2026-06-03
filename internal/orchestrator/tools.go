package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, args map[string]any) (ToolResult, error)
}

type Registry struct {
	root       string
	tools      map[string]Tool
	cache      *ToolCache
	semaphore  *Semaphore
	rateLimiter *RateLimiter
}

func NewRegistry(root string) *Registry {
	return NewRegistryWithOptions(root, OrchestratorOptions{MaxConcurrentTools: 8, RateLimitPerSecond: 0, ToolCacheEnabled: true})
}

func NewRegistryWithOptions(root string, opts OrchestratorOptions) *Registry {
	if strings.TrimSpace(root) == "" { root = "." }
	maxConcurrent := opts.MaxConcurrentTools
	if maxConcurrent <= 0 { maxConcurrent = 8 }
	registry := &Registry{root: root, tools: map[string]Tool{}, cache: NewToolCache(), semaphore: NewSemaphore(maxConcurrent)}
	if opts.RateLimitPerSecond > 0 { registry.rateLimiter = NewRateLimiter(opts.RateLimitPerSecond) }
	registry.Register(&ReadFilesTool{root: root, cache: registry.cache})
	registry.Register(&ListDirTool{root: root})
	registry.Register(&SearchTool{root: root})
	registry.Register(&EditFileTool{root: root, cache: registry.cache})
	registry.Register(&ExecuteTool{enabled: opts.ExecuteEnabled})
	return registry
}

func (r *Registry) Register(tool Tool) { if r != nil && tool != nil { r.tools[tool.Name()] = tool } }
func (r *Registry) Tool(name string) Tool { if r == nil { return nil }; return r.tools[name] }

func (r *Registry) RunParallel(ctx context.Context, calls []ToolCall) []ToolResult {
	if len(calls) == 0 { return nil }
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		i, call := i, call
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.rateLimiter != nil {
				if err := r.rateLimiter.Wait(ctx); err != nil { results[i] = ToolResult{Name: call.Name, Call: call, Error: err.Error()}; return }
			}
			if err := r.semaphore.Acquire(ctx); err != nil { results[i] = ToolResult{Name: call.Name, Call: call, Error: err.Error()}; return }
			defer r.semaphore.Release()
			tool := r.tools[call.Name]
			if tool == nil { results[i] = ToolResult{Name: call.Name, Call: call, Error: "tool not found"}; return }
			result, err := tool.Execute(ctx, call.Args)
			result.Name = call.Name; result.Call = call
			if err != nil { result.Error = err.Error() }
			results[i] = result
		}()
	}
	wg.Wait()
	return results
}

func (r *Registry) PrefetchAsync(ctx context.Context, hint *PrefetchHint) <-chan ToolResult {
	ch := make(chan ToolResult, 1)
	go func() {
		defer close(ch)
		if hint == nil || len(hint.Paths) == 0 { return }
		// Prefetch uses direct I/O without semaphore to avoid deadlock under high concurrency.
		// Prefetch is best-effort; the main RunParallel path will re-read if prefetch misses.
		call := ToolCall{Name: "read_files", Args: map[string]any{"paths": hint.Paths}}
		tool := r.tools[call.Name]
		if tool == nil { return }
		result, err := tool.Execute(ctx, call.Args)
		result.Name = call.Name; result.Call = call
		if err != nil { result.Error = err.Error() }
		select { case ch <- result: default: }
	}()
	return ch
}

type Semaphore struct { ch chan struct{} }
func NewSemaphore(max int) *Semaphore { if max <= 0 { max = 1 }; return &Semaphore{ch: make(chan struct{}, max)} }
func (s *Semaphore) Acquire(ctx context.Context) error { if s == nil { return nil }; select { case s.ch <- struct{}{}: return nil; case <-ctx.Done(): return ctx.Err() } }
func (s *Semaphore) Release() { if s != nil { select { case <-s.ch: default: } } }

type RateLimiter struct { interval time.Duration; mu sync.Mutex; next time.Time }
func NewRateLimiter(ratePerSecond int) *RateLimiter { if ratePerSecond <= 0 { ratePerSecond = 1 }; return &RateLimiter{interval: time.Second / time.Duration(ratePerSecond)} }
func (l *RateLimiter) Wait(ctx context.Context) error {
	if l == nil { return nil }
	l.mu.Lock(); wait := time.Until(l.next); if wait < 0 { wait = 0 }; l.next = time.Now().Add(wait + l.interval); l.mu.Unlock()
	if wait == 0 { return nil }
	t := time.NewTimer(wait); defer t.Stop()
	select { case <-t.C: return nil; case <-ctx.Done(): return ctx.Err() }
}

type ReadFilesTool struct{ root string; cache *ToolCache }
func (t *ReadFilesTool) Name() string { return "read_files" }
func (t *ReadFilesTool) Description() string { return "Read one or many files; supports glob patterns." }
func (t *ReadFilesTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string"}}},"required":["paths"]}`) }
func (t *ReadFilesTool) Execute(ctx context.Context, args map[string]any) (ToolResult, error) {
	paths := stringListArg(args["paths"]); if len(paths) == 0 { return ToolResult{}, errors.New("paths is required") }
	resolved, err := expandPaths(t.root, paths); if err != nil { return ToolResult{}, err }
	var out strings.Builder; files := make([]string, 0, len(resolved)); hits := 0
	for _, path := range resolved {
		select { case <-ctx.Done(): return ToolResult{Content: out.String()}, ctx.Err(); default: }
		if cached, ok := t.cache.Get(path); ok { out.WriteString(cached.Content); if !strings.HasSuffix(cached.Content, "\n") { out.WriteString("\n") }; hits++; files = append(files, path); continue }
		data, err := os.ReadFile(path)
		var section strings.Builder
		if err != nil { fmt.Fprintf(&section, "## %s\nERROR: %v\n\n", path, err) } else { fmt.Fprintf(&section, "## %s\n%s\n\n", path, string(data)); files = append(files, path) }
		content := section.String(); out.WriteString(content)
		if err == nil { t.cache.Set(path, ToolResult{Content: content, Metadata: map[string]any{"files": []string{path}}}) }
	}
	return ToolResult{Content: strings.TrimSpace(out.String()), Metadata: map[string]any{"files": files, "cache_hits": hits}}, nil
}

type ListDirTool struct{ root string }
func (t *ListDirTool) Name() string { return "list_dir" }
func (t *ListDirTool) Description() string { return "List a directory with file size preview." }
func (t *ListDirTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`) }
func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) (ToolResult, error) {
	path := strings.TrimSpace(stringArg(args["path"])); if path == "" { path = "." }
	resolved, err := safeJoin(t.root, path); if err != nil { return ToolResult{}, err }
	entries, err := os.ReadDir(resolved); if err != nil { return ToolResult{}, err }
	var out strings.Builder
	for _, entry := range entries { select { case <-ctx.Done(): return ToolResult{Content: out.String()}, ctx.Err(); default: }; info, _ := entry.Info(); typeName := "file"; size := int64(0); if info != nil { size = info.Size(); if info.IsDir() { typeName = "dir" } }; fmt.Fprintf(&out, "%s\t%s\t%d\n", typeName, entry.Name(), size) }
	return ToolResult{Content: strings.TrimSpace(out.String()), Metadata: map[string]any{"path": resolved}}, nil
}

type SearchTool struct{ root string }
func (t *SearchTool) Name() string { return "search" }
func (t *SearchTool) Description() string { return "Search files and return snippets with line numbers." }
func (t *SearchTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"required":["query"]}`) }
func (t *SearchTool) Execute(ctx context.Context, args map[string]any) (ToolResult, error) {
	query := strings.TrimSpace(stringArg(args["query"])); if query == "" { return ToolResult{}, errors.New("query is required") }
	paths := stringListArg(args["paths"]); if len(paths) == 0 { paths = []string{"."} }
	roots, err := expandPaths(t.root, paths); if err != nil { return ToolResult{}, err }
	var out strings.Builder; matches := 0
	for _, root := range roots {
		info, statErr := os.Stat(root); if statErr != nil { continue }
		walkRoot := root
		if !info.IsDir() { walkRoot = filepath.Dir(root) }
		err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() || matches >= 100 { return nil }
			if !info.IsDir() && path != root { return nil }
			select { case <-ctx.Done(): return ctx.Err(); default: }
			data, err := os.ReadFile(path); if err != nil { return nil }
			lines := strings.Split(string(data), "\n")
			for i, line := range lines { if strings.Contains(strings.ToLower(line), strings.ToLower(query)) { fmt.Fprintf(&out, "%s:%d: %s\n", path, i+1, strings.TrimSpace(line)); matches++; if matches >= 100 { break } } }
			return nil
		})
		if err != nil { return ToolResult{Content: out.String()}, err }
	}
	return ToolResult{Content: strings.TrimSpace(out.String()), Metadata: map[string]any{"matches": matches}}, nil
}

type EditFileTool struct{ root string; cache *ToolCache }
func (t *EditFileTool) Name() string { return "edit_file" }
func (t *EditFileTool) Description() string { return "Edit a file by replacing an exact string and invalidate cached reads for that path." }
func (t *EditFileTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"]}`) }
func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) (ToolResult, error) {
	path := strings.TrimSpace(firstString(args, "path", "file_path")); if path == "" { return ToolResult{}, errors.New("path is required") }
	oldString := stringArg(args["old_string"]); newString := stringArg(args["new_string"]); if oldString == "" { return ToolResult{}, errors.New("old_string is required") }
	resolved, err := safeJoin(t.root, path); if err != nil { return ToolResult{}, err }
	select { case <-ctx.Done(): return ToolResult{}, ctx.Err(); default: }
	data, err := os.ReadFile(resolved); if err != nil { return ToolResult{}, err }
	content := string(data); count := strings.Count(content, oldString); if count == 0 { return ToolResult{}, errors.New("old_string not found") }; if count > 1 && !boolArg(args["replace_all"]) { return ToolResult{}, errors.New("old_string is not unique; set replace_all to true") }
	if boolArg(args["replace_all"]) { content = strings.ReplaceAll(content, oldString, newString) } else { content = strings.Replace(content, oldString, newString, 1) }
	if err := os.WriteFile(resolved, []byte(content), 0644); err != nil { return ToolResult{}, err }
	t.cache.Invalidate(resolved)
	return ToolResult{Content: fmt.Sprintf("edited %s (%d replacement%s)", resolved, count, plural(count)), Metadata: map[string]any{"path": resolved, "replacements": count}}, nil
}

type ExecuteTool struct{ enabled bool }
func (t *ExecuteTool) Name() string { return "execute" }
func (t *ExecuteTool) Description() string { return "Command execution is hard-off until security review." }
func (t *ExecuteTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`) }
func (t *ExecuteTool) Execute(ctx context.Context, args map[string]any) (ToolResult, error) { return ToolResult{Content: `{"error":"execute_disabled","message":"execute is hard-off pending security review"}`, Metadata: map[string]any{"rejected": true, "reason": "execute_disabled"}}, errors.New("execute is hard-off pending security review") }

func stringArg(value any) string { if s, ok := value.(string); ok { return s }; return "" }
func firstString(args map[string]any, keys ...string) string { for _, key := range keys { if v := strings.TrimSpace(stringArg(args[key])); v != "" { return v } }; return "" }
func boolArg(value any) bool { b, _ := value.(bool); return b }
func stringListArg(value any) []string { switch v := value.(type) { case []string: return v; case []any: out := make([]string, 0, len(v)); for _, item := range v { if s := strings.TrimSpace(stringArg(item)); s != "" { out = append(out, s) } }; return out; case string: if strings.TrimSpace(v) != "" { return []string{strings.TrimSpace(v)} } }; return nil }
func expandPaths(root string, paths []string) ([]string, error) { out := []string{}; seen := map[string]bool{}; for _, raw := range paths { clean := strings.TrimSpace(raw); if clean == "" { continue }; if strings.ContainsAny(clean, "*?[") || strings.Contains(clean, "{") { pattern, err := safeJoin(root, clean); if err != nil { return nil, err }; matches, err := filepath.Glob(pattern); if err != nil { return nil, err }; for _, m := range matches { if !seen[m] { out = append(out, m); seen[m] = true } }; continue }; resolved, err := safeJoin(root, clean); if err != nil { return nil, err }; if !seen[resolved] { out = append(out, resolved); seen[resolved] = true } }; sort.Strings(out); return out, nil }
func safeJoin(root string, path string) (string, error) {
	base, err := filepath.Abs(root)
	if err != nil { return "", err }
	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Clean(filepath.Join(base, path))
	}
	// Resolve symlinks to prevent traversal via symlink
	if eval, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil { resolved = eval }
	// Boundary check: resolved must be base or under base
	if resolved != base && !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes root: %s", path)
	}
	return resolved, nil
}
func plural(count int) string { if count == 1 { return "" }; return "s" }
