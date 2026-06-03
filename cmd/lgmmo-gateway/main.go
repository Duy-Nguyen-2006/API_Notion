// lgmmo-gateway: unified reverse proxy for api.lgmmo.click
// Routes /v1/chat/completions by model field:
//   mimo-*  → cliproxy (default :8317)
//   *       → notion2api (default :8787)
//
// All other paths → notion2api.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	listenAddr = flag.String("listen", ":8300", "address to listen on")
	notionAddr = flag.String("notion-upstream", "http://127.0.0.1:8787", "notion2api upstream")
	mimoAddr   = flag.String("mimo-upstream", "http://127.0.0.1:8317", "cliproxy/mimo upstream")
	healthFile = flag.String("health-file", "", "optional file to write last-upstream health status")
)

// notionModelPrefixes are model IDs that should be routed to notion2api.
// Everything matching these (or having no model / "auto") → notion2api.
var notionModelPrefixes = []string{
	"auto", "gpt-", "gemini-", "opus-", "sonnet-", "haiku-", "grok-", "minimax-",
	"claude-", "ambrosia-", "apricot-", "avocado-", "almond-", "oatmeal-",
	"oval-", "oregon-", "otaheite-", "xigua-", "fireworks-", "vertex-",
	"galette-", "gingerbread-", "opal-",
}

type gateway struct {
	notion *httputil.ReverseProxy
	mimo   *httputil.ReverseProxy
}

func newGateway() *gateway {
	notionURL, _ := url.Parse(*notionAddr)
	mimoURL, _ := url.Parse(*mimoAddr)
	return &gateway{
		notion: httputil.NewSingleHostReverseProxy(notionURL),
		mimo:   httputil.NewSingleHostReverseProxy(mimoURL),
	}
}

// isNotionModel returns true if model should go to notion2api.
func isNotionModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if model == "" || model == "auto" || model == "default" {
		return true // default → notion2api orchestrator
	}
	for _, prefix := range notionModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func (gw *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Non-chat paths always → notion2api
	if !isChatCompletionsPath(r.URL.Path) {
		log.Printf("%s %s → notion2api (non-chat)", r.Method, r.URL.Path)
		gw.notion.ServeHTTP(w, r)
		return
	}

	// Only intercept POST with JSON body for model routing
	if r.Method != http.MethodPost || !isJSONContentType(r) {
		log.Printf("%s %s → notion2api (non-POST/non-JSON)", r.Method, r.URL.Path)
		gw.notion.ServeHTTP(w, r)
		return
	}

	// Read body to extract model field
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	model := extractModel(body)

	// Clone request with replayed body
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	if isNotionModel(model) {
		log.Printf("%s %s model=%q → notion2api (%s)", r.Method, r.URL.Path, model, time.Since(start))
		gw.notion.ServeHTTP(w, r)
	} else {
		log.Printf("%s %s model=%q → mimo (%s)", r.Method, r.URL.Path, model, time.Since(start))
		gw.mimo.ServeHTTP(w, r)
	}
}

func isChatCompletionsPath(path string) bool {
	return path == "/v1/chat/completions" || path == "/chat/completions"
}

func isJSONContentType(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.Contains(ct, "application/json")
}

func extractModel(body []byte) string {
	// Fast path: scan for "model":"..." without full JSON parse
	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return envelope.Model
}

func main() {
	flag.Parse()
	gw := newGateway()

	// Wrap with CORS
	handler := corsMiddleware(gw)

	log.Printf("lgmmo-gateway listening on %s", *listenAddr)
	log.Printf("  notion2api upstream: %s", *notionAddr)
	log.Printf("  mimo upstream:       %s", *mimoAddr)
	log.Printf("  routing: mimo-* → mimo, everything else → notion2api")

	if err := http.ListenAndServe(*listenAddr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func usage() {
	fmt.Fprintf(os.Stderr, `lgmmo-gateway: unified reverse proxy for api.lgmmo.click

Routes /v1/chat/completions by model field:
  mimo-*     → cliproxy (default :8317)
  everything → notion2api (default :8787)

All other paths (healthz, models, admin) → notion2api.

`)
	flag.PrintDefaults()
}
