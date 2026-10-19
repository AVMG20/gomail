package fakes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// OpenRouter fakes the OpenRouter endpoints gomail uses: the Decisions API
// (classification), the model list and chat completions.
type OpenRouter struct {
	Server *httptest.Server
	// Score returns P(yes) for a label question ("label_<id>") given the
	// message state (subject, body, from, ...). Default: 0.05.
	Score func(question string, state map[string]any) float64

	mu        sync.Mutex
	decisions int
	chats     int
	keys      map[string]int // bearer keys seen
}

// Calls returns the number of Decisions and chat calls, and how many
// requests carried the given API key.
func (o *OpenRouter) Calls(key string) (decisions, chats, withKey int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.decisions, o.chats, o.keys[key]
}

// NewOpenRouter starts the fake. Pass its URL as core Options.OpenRouterBase.
func NewOpenRouter() *OpenRouter {
	o := &OpenRouter{keys: map[string]int{}}
	o.Server = httptest.NewServer(http.HandlerFunc(o.serve))
	return o
}

// Close stops the server.
func (o *OpenRouter) Close() { o.Server.Close() }

// URL is the base URL.
func (o *OpenRouter) URL() string { return o.Server.URL }

func (o *OpenRouter) serve(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	o.keys[bearer(r)]++
	o.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/decisions"):
		var req struct {
			Questions map[string]any `json:"questions"`
			State     map[string]any `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Questions) == 0 {
			writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "message": "questions required"}})
			return
		}
		answers := map[string]any{}
		for k := range req.Questions {
			p := 0.05
			if o.Score != nil {
				p = o.Score(k, req.State)
			}
			answers[k] = map[string]any{"type": "noul", "noul": p}
		}
		o.mu.Lock()
		o.decisions++
		o.mu.Unlock()
		writeJSON(w, 200, map[string]any{"answers": answers, "usage": map[string]any{"cost": 0.00002}})
	case strings.HasSuffix(r.URL.Path, "/models"):
		writeJSON(w, 200, map[string]any{"data": []any{
			map[string]any{"id": "typesafe/jev-1.13", "name": "Jev", "pricing": map[string]string{"prompt": "0.0000002", "completion": "0"}},
			map[string]any{"id": "openai/gpt-4o-mini", "name": "GPT-4o mini", "pricing": map[string]string{"prompt": "0.00000015", "completion": "0.0000006"}},
		}})
	case strings.HasSuffix(r.URL.Path, "/key"):
		if !strings.HasPrefix(bearer(r), "sk-or-") {
			writeJSON(w, 401, map[string]any{"error": map[string]any{"code": 401, "message": "No auth credentials found"}})
			return
		}
		writeJSON(w, 200, map[string]any{"data": map[string]any{"label": "fake key"}})
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		o.mu.Lock()
		o.chats++
		o.mu.Unlock()
		writeJSON(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "Fake summary."}}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 3, "cost": 0.00001}})
	default:
		http.NotFound(w, r)
	}
}
