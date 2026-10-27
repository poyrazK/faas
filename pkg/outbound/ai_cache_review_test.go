package outbound

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResponseCacheKeyIgnoresTelemetryHeaders(t *testing.T) {
	cache, err := newOutboundResponseCache()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"gpt-4o"}`)
	keyWith := func(headers map[string]string) string {
		req := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", nil)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		return cache.key("integration-1", "app-1", 1, 60, req, body)
	}
	first := keyWith(map[string]string{"Traceparent": "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01", "User-Agent": "OpenAI/Python 2.1",
		"X-Stainless-Retry-Count": "0", "Baggage": "a=1", "Anthropic-Version": "2023-06-01"})
	second := keyWith(map[string]string{"Traceparent": "00-cccccccccccccccccccccccccccccccc-dddddddddddddddd-01", "User-Agent": "OpenAI/Python 2.2",
		"X-Stainless-Retry-Count": "1", "Sentry-Trace": "x", "Anthropic-Version": "2023-06-01"})
	if first != second {
		t.Fatal("per-call telemetry headers changed the cache key")
	}
	if other := keyWith(map[string]string{"Anthropic-Version": "2024-01-01"}); other == first {
		t.Fatal("a header that can change the answer was left out of the cache key")
	}
}

func TestModerationSkipsResponseCacheHits(t *testing.T) {
	f := newPhase8Fixture(t, func(integrations map[string]*Integration) {
		integrations["primary"].AIContentPolicy = moderationPolicy(api.OutboundAIModerationKindClassifier, api.OutboundAIModerationFlag,
			api.OutboundAIModerationOnErrorAllow, api.OutboundAIModerationHarassment)
		integrations["primary"].ResponseCacheTTLSeconds = 60
	})
	body := `{"model":"gpt-current","messages":[{"role":"user","content":"hello"}]}`
	for i, traceparent := range []string{"00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01", "00-cccccccccccccccccccccccccccccccc-dddddddddddddddd-01"} {
		if resp := f.post("/i/primary/v1/chat/completions", body, map[string]string{"Traceparent": traceparent}); resp.Code != http.StatusOK {
			t.Fatalf("call %d = %d %s", i, resp.Code, resp.Body.String())
		}
	}
	if calls := f.calls(); len(calls) != 2 || !strings.HasSuffix(calls[0], " judge") {
		t.Fatalf("provider calls = %q; want one moderation check and one model call, the second call served from the cache", calls)
	}
}
