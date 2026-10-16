package outbound

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func aiRequestFor(t *testing.T, provider, path, contentType, body string) (*aiRequest, *http.Request) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://gateway.test/i/x"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	ai, _, err := readAIRequest(r, Integration{AIProvider: provider}, path)
	if err != nil {
		t.Fatal(err)
	}
	return ai, r
}

func TestReadAIRequestInjectsOpenAIStreamUsage(t *testing.T) {
	ai, _ := aiRequestFor(t, api.OutboundAIProviderOpenAI, "/v1/chat/completions", "application/json",
		`{"model":"gpt-4o","stream":true,"stream_options":{"foo":1},"messages":[]}`)
	if ai == nil || !ai.streaming || !ai.usageInjected || !bytes.Contains(ai.body, []byte(`"include_usage":true`)) || !bytes.Contains(ai.body, []byte(`"foo":1`)) {
		t.Fatalf("injected request = %+v body=%s", ai, ai.body)
	}
	for name, tc := range map[string]struct{ provider, path, body string }{
		"caller already asked": {api.OutboundAIProviderOpenAI, "/v1/chat/completions", `{"stream":true,"stream_options":{"include_usage":true}}`},
		"not streaming":        {api.OutboundAIProviderOpenAI, "/v1/chat/completions", `{"stream":false}`},
		"other endpoint":       {api.OutboundAIProviderOpenAI, "/v1/embeddings", `{"stream":true}`},
		"anthropic":            {api.OutboundAIProviderAnthropic, "/v1/messages", `{"stream":true}`},
	} {
		ai, _ := aiRequestFor(t, tc.provider, tc.path, "application/json", tc.body)
		if ai == nil || ai.usageInjected || string(ai.body) != tc.body {
			t.Fatalf("%s: request = %+v body=%s", name, ai, ai.body)
		}
	}
}

func TestReadAIRequestRestoresOversizedBodies(t *testing.T) {
	large := `{"pad":"` + strings.Repeat("x", int(api.OutboundAIRequestBufferMaxBytes)) + `"}`
	ai, r := aiRequestFor(t, api.OutboundAIProviderOpenAI, "/v1/chat/completions", "application/json", large)
	if ai != nil {
		t.Fatal("oversized body was buffered")
	}
	got, err := io.ReadAll(r.Body)
	if err != nil || string(got) != large {
		t.Fatalf("restored body length = %d, err = %v", len(got), err)
	}
	if ai, _ := aiRequestFor(t, "", "/v1/chat/completions", "application/json", `{}`); ai != nil {
		t.Fatal("non-AI integration buffered a body")
	}
}

func TestUsageChunkFilterHidesOnlyTheInjectedChunk(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\r\n\r\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n" +
		"data: [DONE]\n\n"
	got, err := io.ReadAll(newUsageChunkFilter(io.NopCloser(strings.NewReader(stream))))
	if err != nil {
		t.Fatal(err)
	}
	want := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\r\n\r\ndata: [DONE]\n\n"
	if string(got) != want {
		t.Fatalf("filtered stream = %q", got)
	}
	// The first event outgrows the bound before its blank line arrives, so the
	// filter switches to pass-through and stops hiding later chunks.
	oversized := "data: " + strings.Repeat("y", api.OutboundAIUsageEventMaxBytes+100_000) + "\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1}}\n\n"
	passthrough, _ := io.ReadAll(newUsageChunkFilter(io.NopCloser(strings.NewReader(oversized))))
	if string(passthrough) != oversized {
		t.Fatal("filter held back or changed a stream after an oversized event")
	}
}

type recordingProvider struct {
	mu     sync.Mutex
	bodies []string
}

func (p *recordingProvider) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.bodies = append(p.bodies, string(body))
	p.mu.Unlock()
}

func (p *recordingProvider) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.bodies...)
}

func aiPolicyHandler(t *testing.T, upstream *httptest.Server, provider string, configure func(*Integration)) (*Handler, *MemoryBackend) {
	t.Helper()
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = provider
	if configure != nil {
		configure(&integration)
	}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	return handler, backend
}

func postAI(handler *Handler, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := gatewayRequest("/i/integration-1"+path, "token", "app-1", http.MethodPost, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAIPostRetriesReplayTheBody(t *testing.T) {
	provider := &recordingProvider{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.record(r)
		if len(provider.calls()) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	handler, backend := aiPolicyHandler(t, upstream, api.OutboundAIProviderOpenAI, func(i *Integration) { i.MaxRetries = 1 })
	resp := postAI(handler, "/v1/chat/completions", `{"model":"m","messages":[]}`)
	calls := provider.calls()
	if resp.Code != http.StatusOK || len(calls) != 2 || calls[0] != calls[1] || calls[1] != `{"model":"m","messages":[]}` {
		t.Fatalf("status=%d calls=%q", resp.Code, calls)
	}
	if backend.DailyAITokenCount("integration-1") != 3 {
		t.Fatalf("retried call settled %d tokens", backend.DailyAITokenCount("integration-1"))
	}
}

func TestAIPostCacheMatchesExactBodies(t *testing.T) {
	provider := &recordingProvider{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	handler, backend := aiPolicyHandler(t, upstream, api.OutboundAIProviderOpenAI, func(i *Integration) { i.ResponseCacheTTLSeconds = 60 })
	for i := 0; i < 2; i++ {
		if resp := postAI(handler, "/v1/chat/completions", `{"model":"m","messages":[1]}`); resp.Code != http.StatusOK {
			t.Fatalf("call %d status = %d", i, resp.Code)
		}
	}
	if got := len(provider.calls()); got != 1 {
		t.Fatalf("identical POSTs reached the provider %d times", got)
	}
	postAI(handler, "/v1/chat/completions", `{"model":"m","messages":[2]}`)
	postAI(handler, "/v1/chat/completions", `{"model":"m","stream":true,"messages":[1]}`)
	if got := len(provider.calls()); got != 3 {
		t.Fatalf("different or streaming bodies were served from cache: %d provider calls", got)
	}
	// Three provider calls of 3 tokens each; the cache hit adds none.
	if backend.DailyAITokenCount("integration-1") != 9 {
		t.Fatalf("cache hit was charged tokens: %d", backend.DailyAITokenCount("integration-1"))
	}
}

func TestOpenAIStreamUsageIsCountedButHiddenFromTheCaller(t *testing.T) {
	provider := &recordingProvider{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\n"+
			"data: {\"model\":\"gpt-4o\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\n"+
			"data: [DONE]\n\n")
	}))
	defer upstream.Close()
	handler, backend := aiPolicyHandler(t, upstream, api.OutboundAIProviderOpenAI, nil)
	resp := postAI(handler, "/v1/chat/completions", `{"model":"gpt-4o","stream":true,"messages":[]}`)
	if resp.Code != http.StatusOK || strings.Contains(resp.Body.String(), "prompt_tokens") || !strings.Contains(resp.Body.String(), "[DONE]") {
		t.Fatalf("caller stream = %d %q", resp.Code, resp.Body.String())
	}
	if calls := provider.calls(); len(calls) != 1 || !strings.Contains(calls[0], `"include_usage":true`) {
		t.Fatalf("provider request = %q", calls)
	}
	if got := backend.DailyAITokenCount("integration-1"); got != 9 {
		t.Fatalf("hidden usage chunk settled %d tokens, want 9", got)
	}
}
