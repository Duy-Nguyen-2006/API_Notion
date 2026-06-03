package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"notion2api/internal/orchestrator"
)

type appModelClient struct {
	app     *App
	r       *http.Request
	request PromptRunRequest
}

func (c appModelClient) Chat(ctx context.Context, messages []orchestrator.Message) (orchestrator.ModelResult, error) {
	req := c.request
	req.Prompt = orchestratorMessagesPrompt(messages)
	if strings.TrimSpace(req.Prompt) == "" {
		req.Prompt = c.request.Prompt
	}
	result, err := c.app.runPrompt(c.r, req)
	return orchestrator.ModelResult{Text: result.Text}, err
}

func (c appModelClient) ChatStream(ctx context.Context, messages []orchestrator.Message, w orchestrator.StreamWriter) (orchestrator.ModelResult, error) {
	req := c.request
	req.Prompt = orchestratorMessagesPrompt(messages)
	if strings.TrimSpace(req.Prompt) == "" {
		req.Prompt = c.request.Prompt
	}
	result, err := c.app.runPromptStream(c.r, req, func(delta string) error {
		if w == nil {
			return nil
		}
		return w.WriteDelta(delta)
	})
	return orchestrator.ModelResult{Text: result.Text}, err
}

func orchestratorMessagesPrompt(messages []orchestrator.Message) string {
	var out strings.Builder
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		fmt.Fprintf(&out, "[%s]\n%s\n\n", role, content)
	}
	return strings.TrimSpace(out.String())
}

var orchestratorModelIDs = []string{"auto", "opus-4.8"}

func shouldUseOrchestratorChat(typed chatCompletionsRequestBody, payload map[string]any, requestedModelID string) bool {
	cleaned := strings.ToLower(strings.TrimSpace(requestedModelID))
	for _, id := range orchestratorModelIDs {
		if cleaned == id {
			return true
		}
	}
	if enabled, ok := parseAgentConfigEnabled(typed.AgentConfig); ok {
		return enabled
	}
	if payload != nil {
		if enabled, ok := parseAgentConfigEnabled(payload["agent_config"]); ok {
			return enabled
		}
	}
	return false
}

func parseAgentConfigEnabled(raw any) (bool, bool) {
	cfg := decodeJSONObjectAny(raw)
	if cfg == nil {
		return false, false
	}
	return parseBoolField(cfg["enabled"])
}

func orchestratorRequestFromPrompt(prompt string) orchestrator.UserRequest {
	return orchestrator.UserRequest{
		Prompt: strings.TrimSpace(prompt),
		Messages: []orchestrator.Message{{
			Role:    "user",
			Content: strings.TrimSpace(prompt),
		}},
		RootDir: ".",
	}
}

func buildChatCompletionFromOrchestrator(result orchestrator.RunResult, prompt string, modelID string, includeTrace bool) map[string]any {
	payload := buildChatCompletion(InferenceResult{Prompt: prompt, Text: result.Text}, modelID, includeTrace)
	payload["agent_trace"] = result.Trace
	return payload
}

type chatCompletionSSEWriter struct {
	w            http.ResponseWriter
	flusher      http.Flusher
	completionID string
	created      int64
	modelID      string
	mu           sync.Mutex
	emitted       strings.Builder
}

func (w *chatCompletionSSEWriter) WriteDelta(delta string) error {
	if delta == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.emitted.WriteString(delta)
	return writeSSEData(w.w, w.flusher, buildChatStreamChunk(w.completionID, w.created, w.modelID, []map[string]any{
		buildChatStreamDeltaChoice(0, map[string]any{"content": delta}),
	}, nil))
}

func (w *chatCompletionSSEWriter) Text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.emitted.String()
}

// validateOrchestratorConfig checks whether the orchestrator can fulfil the
// request given the current application state. It returns a user-facing error
// if the configuration is insufficient (e.g. model "auto" but no upstream
// session is available to act as the reasoner).
func (a *App) validateOrchestratorConfig(modelID string) error {
	cleaned := strings.ToLower(strings.TrimSpace(modelID))
	for _, id := range orchestratorModelIDs {
		if cleaned == id {
			a.State.mu.RLock()
			hasClient := a.State.Client != nil
			a.State.mu.RUnlock()
			if !hasClient {
				return fmt.Errorf("orchestrator model %q requires a configured Notion session (reasoner client unavailable)", modelID)
			}
			break
		}
	}
	return nil
}

func (a *App) handleOrchestratorChatCompletions(w http.ResponseWriter, r *http.Request, request PromptRunRequest, modelID string, includeUsage bool, conversationID string, stream bool) {
	if err := a.validateOrchestratorConfig(modelID); err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, err.Error(), "invalid_request_error", "orchestrator_config_invalid")
		return
	}
	client := appModelClient{app: a, r: r, request: request}
	controller := orchestrator.NewController(".", client, client)
	controller.Metrics = globalOrchestratorMetrics
	userReq := orchestratorRequestFromPrompt(request.Prompt)
	if stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeOpenAIError(w, http.StatusInternalServerError, "streaming is not supported by this response writer", "api_error", "stream_unsupported")
			return
		}
		completionID := "chatcmpl-" + strings.ReplaceAll(randomUUID(), "-", "")
		created := time.Now().Unix()
		prepareOpenAISSEHeaders(w)
		a.markConversationEnvelope(conversationID, "", completionID)
		if err := writeSSEData(w, flusher, buildChatStreamChunk(completionID, created, modelID, []map[string]any{
			buildChatStreamDeltaChoice(0, map[string]any{"role": "assistant"}),
		}, nil)); err != nil {
			return
		}
		streamWriter := &chatCompletionSSEWriter{w: w, flusher: flusher, completionID: completionID, created: created, modelID: modelID}
		result, err := controller.Run(r.Context(), userReq, streamWriter)
		if err != nil {
			a.failConversation(conversationID, err)
			_ = writeSSEData(w, flusher, map[string]any{"error": map[string]any{"message": err.Error(), "type": "api_error", "param": nil, "code": "orchestrator_error"}})
			writeSSEDone(w, flusher)
			return
		}
		if remaining := textDeltaSuffix(streamWriter.Text(), result.Text); remaining != "" {
			_ = streamWriter.WriteDelta(remaining)
		}
		finalUsage := map[string]any{}
		if includeUsage {
			finalUsage = buildUsage(request.Prompt, result.Text, "")
		}
		_ = writeSSEData(w, flusher, buildChatStreamChunk(completionID, created, modelID, []map[string]any{buildChatStreamFinishChoice(0, "stop")}, finalUsage))
		_ = writeSSEData(w, flusher, map[string]any{"agent_trace": result.Trace})
		writeSSEDone(w, flusher)
		inference := InferenceResult{Prompt: request.Prompt, Text: result.Text}
		a.completeConversation(conversationID, inference)
		a.persistConversationSession(conversationID, request, inference)
		return
	}

	result, err := controller.Run(r.Context(), userReq, nil)
	if err != nil {
		a.failConversation(conversationID, err)
		a.writeUpstreamError(w, err)
		return
	}
	inference := InferenceResult{Prompt: request.Prompt, Text: result.Text}
	payload := buildChatCompletionFromOrchestrator(result, request.Prompt, modelID, false)
	attachConversationResponseMetadata(payload, conversationID, "")
	a.markConversationEnvelope(conversationID, "", stringValue(payload["id"]))
	a.completeConversation(conversationID, inference)
	a.persistConversationSession(conversationID, request, inference)
	writeJSON(w, http.StatusOK, payload)
}
