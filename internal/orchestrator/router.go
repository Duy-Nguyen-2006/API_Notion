package orchestrator

import (
	"path/filepath"
	"regexp"
	"strings"
)

type Router struct {
	classifier *IntentClassifier
}

type IntentClassifier struct{}

func NewRouter() *Router {
	return &Router{classifier: &IntentClassifier{}}
}

func (r *Router) Decide(req UserRequest) Decision {
	if r == nil || r.classifier == nil {
		r = NewRouter()
	}
	return r.classifier.Decide(req)
}

func (c *IntentClassifier) Decide(req UserRequest) Decision {
	prompt := strings.TrimSpace(req.Prompt)
	lower := strings.ToLower(prompt)
	paths := extractPathHints(prompt)
	base := Decision{Intent: IntentSimpleChat, Complexity: ComplexityLow, MaxIterations: 1}

	if isEditIntent(lower) && len(paths) > 0 {
		return Decision{Intent: IntentFileEdit, Complexity: ComplexityMedium, NeedsTools: true, NeedsReasoner: false, Tools: []string{"edit_file"}, MaxIterations: 1, DirectToolPlan: []ToolCall{{Name: "edit_file", Args: map[string]any{"path": paths[0]}}}}
	}

	if isSearchIntent(lower) {
		query := extractSearchQuery(prompt)
		if query == "" {
			query = prompt
		}
		call := ToolCall{Name: "search", Args: map[string]any{"query": query}}
		if len(paths) > 0 {
			call.Args["paths"] = paths
		}
		return Decision{Intent: IntentSearch, Complexity: ComplexityMedium, NeedsTools: true, NeedsReasoner: needsReasoner(lower), Tools: []string{"search"}, MaxIterations: 1, DirectToolPlan: []ToolCall{call}}
	}

	if isListIntent(lower) {
		listPath := "."
		if len(paths) > 0 {
			listPath = paths[0]
		}
		return Decision{Intent: IntentListDir, Complexity: ComplexityMedium, NeedsTools: true, NeedsReasoner: needsReasoner(lower), Tools: []string{"list_dir"}, MaxIterations: 1, DirectToolPlan: []ToolCall{{Name: "list_dir", Args: map[string]any{"path": listPath}}}}
	}

	if len(paths) > 0 && (isReadIntent(lower) || containsGlob(paths) || isCodeAnalysisIntent(lower)) {
		intent := IntentFileRead
		complexity := ComplexityMedium
		tools := []string{"read_files"}
		reason := needsReasoner(lower)
		if isCodeAnalysisIntent(lower) {
			intent = IntentCodeAnalysis
			complexity = ComplexityHigh
			reason = true
			tools = []string{"read_files", "search", "list_dir"}
		} else if reason {
			intent = IntentFileReadSummary
		}
		return Decision{Intent: intent, Complexity: complexity, NeedsTools: true, NeedsReasoner: reason, Tools: tools, MaxIterations: 1, Prefetch: &PrefetchHint{Paths: paths}, DirectToolPlan: []ToolCall{{Name: "read_files", Args: map[string]any{"paths": paths}}}}
	}

	if isCodeAnalysisIntent(lower) {
		return Decision{Intent: IntentCodeAnalysis, Complexity: ComplexityHigh, NeedsTools: true, NeedsReasoner: true, Tools: []string{"list_dir", "read_files", "search"}, MaxIterations: 2}
	}

	return base
}

func isReadIntent(lower string) bool {
	return containsAny(lower, "read", "cat", "show", "view", "xem", "đọc", "doc")
}

func isEditIntent(lower string) bool {
	return containsAny(lower, "edit", "modify", "change", "update", "replace", "sửa", "sua", "thay đổi", "thay doi")
}

func isListIntent(lower string) bool {
	return containsAny(lower, "list", "ls", "dir", "directory", "folder", "liệt kê", "liet ke", "danh sách", "thu muc", "thư mục")
}

func isSearchIntent(lower string) bool {
	return containsAny(lower, "search", "grep", "find", "tìm", "tim", "kiếm", "kiem")
}

func isCodeAnalysisIntent(lower string) bool {
	return containsAny(lower, "analyze", "analysis", "review", "refactor", "suggest", "phân tích", "phan tich", "đề xuất", "de xuat")
}

func needsReasoner(lower string) bool {
	return isCodeAnalysisIntent(lower) || containsAny(lower, "why", "how", "compare", "summary", "summarize", "explain", "review", "analyze", "vì sao", "tai sao", "tại sao")
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

var pathHintPattern = regexp.MustCompile(`(?i)(?:\./|\.\./|/|[[:alnum:]_.-]+/)[[:alnum:]_./*?{}\[\]-]+|[[:alnum:]_.-]+\.(?:go|md|json|yaml|yml|txt|js|ts|tsx|jsx|css|html|sql|sh|py|rs|java|c|cpp|h)`)

func extractPathHints(prompt string) []string {
	matches := pathHintPattern.FindAllString(prompt, -1)
	seen := map[string]bool{}
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		clean := strings.Trim(match, "`'\".,:;()[]{}")
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		paths = append(paths, clean)
	}
	return paths
}

func containsGlob(paths []string) bool {
	for _, path := range paths {
		if strings.ContainsAny(path, "*?[") || strings.Contains(path, "{") {
			return true
		}
	}
	return false
}

func extractSearchQuery(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	for _, sep := range []string{" for ", " tìm ", " tim ", " kiếm ", " kiem "} {
		parts := strings.SplitN(strings.ToLower(trimmed), sep, 2)
		if len(parts) == 2 {
			idx := strings.Index(strings.ToLower(trimmed), sep)
			if idx >= 0 {
				return strings.Trim(strings.TrimSpace(trimmed[idx+len(sep):]), "`'\"")
			}
		}
	}
	return strings.Trim(filepath.Base(trimmed), "`'\"")
}
