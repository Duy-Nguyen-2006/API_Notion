package orchestrator

import (
	"fmt"
	"strings"
)

type ContextManager struct {
	MaxChars int
}

func NewContextManager(maxChars int) *ContextManager {
	if maxChars <= 0 {
		maxChars = 24000
	}
	return &ContextManager{MaxChars: maxChars}
}

func (m *ContextManager) Distill(results []ToolResult) []Message {
	messages := make([]Message, 0, len(results))
	for _, result := range results {
		var content strings.Builder
		fmt.Fprintf(&content, "Tool: %s\n", result.Name)
		if result.Error != "" {
			fmt.Fprintf(&content, "Error: %s\n", result.Error)
		}
		if result.Content != "" {
			content.WriteString(limitString(result.Content, maxInt(1000, m.MaxChars/len(results)+1)))
		}
		messages = append(messages, Message{Role: "system", Content: strings.TrimSpace(content.String())})
	}
	return messages
}

func (m *ContextManager) BuildReasonerContext(req UserRequest, msgs []Message) []Message {
	budget := m.MaxChars
	out := []Message{{Role: "system", Content: "Use the provided local tool context to answer the user. Do not claim you cannot access files when context is provided."}}
	for _, msg := range msgs {
		if budget <= 0 {
			break
		}
		content := limitString(msg.Content, budget)
		budget -= len(content)
		if strings.TrimSpace(content) != "" {
			out = append(out, Message{Role: msg.Role, Content: content})
		}
	}
	out = append(out, Message{Role: "user", Content: req.Prompt})
	return out
}

func limitString(value string, maxChars int) string {
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[:maxChars] + "\n...[truncated]"
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
