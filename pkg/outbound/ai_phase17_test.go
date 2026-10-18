package outbound

// adr: 1440 — identical streamed AI calls are cached as they stream and
// replayed on a hit; hits settle as cached requests with the cost they
// avoided; and settlement records what prompt caching saved.

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

const streamedChat = "data: {\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
	"data: {\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"model\":\"gpt-5\",\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":500}}\n\n" +
	"data: [DONE]\n\n"

func cachingFixture(t *testing.T, contentType, body string) (*Handler, *MemoryBackend, AIAccessToken, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	upstream := newTLSUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	})
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "llm", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	integration.ResponseCacheTTLSeconds = 60
	handler, backend := phase9Handler(t, upstream.Client(), integration)
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	return handler, backend, token, &calls
}

func TestStreamedResponsesAreCachedAndHitsSettle(t *testing.T) {
	handler, backend, token, calls := cachingFixture(t, "text/event-stream", streamedChat)
	request := `{"model":"gpt-5","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	first := asApp(handler, http.MethodPost, "/i/llm/v1/chat/completions", request, token)
	second := asApp(handler, http.MethodPost, "/i/llm/v1/chat/completions", request, token)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("calls = %d %d, provider calls %d", first.Code, second.Code, calls.Load())
	}
	if second.Body.String() != first.Body.String() || strings.Contains(second.Body.String(), `"usage"`) ||
		!strings.Contains(second.Body.String(), "[DONE]") || second.Header().Get("Age") == "" {
		t.Fatalf("cached stream = %q (first %q) headers %v", second.Body.String(), first.Body.String(), second.Header())
	}
	row, ok := backend.AIUsageRollup("llm", token.AppID, "gpt-5")
	if !ok || row.Requests != 2 || row.CachedRequests != 1 || row.InputTokens != 1000 || row.CostMillicents <= 0 ||
		row.CacheSavedMillicents != row.CostMillicents {
		t.Fatalf("rollup = %+v, %v", row, ok)
	}

	// A different body misses; a request asking for no cache bypasses it.
	other := asApp(handler, http.MethodPost, "/i/llm/v1/chat/completions", strings.Replace(request, "hi", "hey", 1), token)
	if other.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("a different body hit the cache: provider calls %d", calls.Load())
	}
}

func TestWholeResponseHitsSettleTheirSavings(t *testing.T) {
	handler, backend, token, calls := cachingFixture(t, "application/json",
		`{"model":"gpt-5","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3000,"completion_tokens":100}}`)
	request := `{"model":"gpt-5","messages":[]}`
	for range 3 {
		if got := asApp(handler, http.MethodPost, "/i/llm/v1/chat/completions", request, token); got.Code != http.StatusOK {
			t.Fatalf("call = %d", got.Code)
		}
	}
	row, ok := backend.AIUsageRollup("llm", token.AppID, "gpt-5")
	if !ok || calls.Load() != 1 || row.Requests != 3 || row.CachedRequests != 2 || row.CacheSavedMillicents != 2*row.CostMillicents {
		t.Fatalf("rollup = %+v, provider calls %d", row, calls.Load())
	}
}

// failingReader returns its data, then an error.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestCacheTeeKeepsOnlyCompleteStreams(t *testing.T) {
	for _, tc := range []struct {
		name   string
		src    io.Reader
		limit  int64
		read   bool
		stored bool
	}{
		{"complete", strings.NewReader("data: x\n\n"), 1 << 10, true, true},
		{"provider error", &failingReader{data: []byte("data: x\n\n"), err: errors.New("reset")}, 1 << 10, true, false},
		{"too long", strings.NewReader(strings.Repeat("x", 100)), 10, true, false},
		{"abandoned", strings.NewReader("data: x\n\n"), 1 << 10, false, false},
	} {
		var stored []byte
		released := 0
		tee := &cacheTee{src: io.NopCloser(tc.src), limit: tc.limit, store: func(body []byte) { stored = body }, release: func() { released++ }}
		if tc.read {
			_, _ = io.Copy(io.Discard, tee)
		} else {
			_, _ = tee.Read(make([]byte, 3))
		}
		_ = tee.Close()
		if (stored != nil) != tc.stored || released != 1 || (tc.stored && !bytes.Equal(stored, []byte("data: x\n\n"))) {
			t.Errorf("%s: stored %q, released %d", tc.name, stored, released)
		}
	}
}

func TestPromptCacheSavings(t *testing.T) {
	integration := Integration{AIProvider: api.OutboundAIProviderAnthropic, AIPrices: ResolveAIPrices([]api.OutboundAIPriceEntry{{
		ModelPrefix: "claude", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 5_000_000,
		CachedInputMillicentsPerMTok: ptr(int64(100_000)), CacheWriteMillicentsPerMTok: ptr(int64(1_250_000))}})}
	read := AIUsage{Model: "claude-sonnet", Known: true, InputTokens: 10_000, CachedInputTokens: 9_000, OutputTokens: 100}
	cost := aiCallCostMillicents(integration, read)
	if saved := promptCacheSavedMillicents(integration, read, cost); saved != 8_100 {
		t.Fatalf("read savings = %d (cost %d)", saved, cost)
	}
	write := AIUsage{Model: "claude-sonnet", Known: true, InputTokens: 10_000, CacheWriteTokens: 8_000, OutputTokens: 100}
	if saved := promptCacheSavedMillicents(integration, write, aiCallCostMillicents(integration, write)); saved != -2_000 {
		t.Fatalf("write savings = %d", saved)
	}
	if saved := promptCacheSavedMillicents(integration, AIUsage{Model: "claude-sonnet", Known: true, InputTokens: 10}, 1); saved != 0 {
		t.Fatalf("uncached savings = %d", saved)
	}
}

func ptr[T any](value T) *T { return &value }

// newTLSUpstream serves handler over TLS until the test ends.
func newTLSUpstream(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return server
}
