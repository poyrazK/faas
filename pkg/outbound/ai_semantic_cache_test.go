package outbound

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSemanticTurn(t *testing.T) {
	long := strings.Repeat("a", api.OutboundAISemanticCacheMaxInputBytes+1)
	for _, tc := range []struct {
		name   string
		format aiWireFormat
		path   string
		body   string
		want   string
	}{
		{"chat string", aiFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","messages":[{"role":"system","content":"s"},{"role":"user","content":"hi there"}]}`, "hi there"},
		{"chat text parts", aiFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`, "a\nb"},
		{"chat image", aiFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"image_url","image_url":{"url":"x"}}]}]}`, ""},
		{"chat ends with assistant", aiFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}]}`, ""},
		{"chat four user turns", aiFormatOpenAI, "/v1/chat/completions",
			`{"model":"m","messages":[{"role":"user","content":"1"},{"role":"user","content":"2"},{"role":"user","content":"3"},{"role":"user","content":"4"}]}`, ""},
		{"chat too long", aiFormatOpenAI, "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"` + long + `"}]}`, ""},
		{"chat blank", aiFormatOpenAI, "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"  "}]}`, ""},
		{"responses string", aiFormatOpenAI, "/v1/responses", `{"model":"m","input":"what now"}`, "what now"},
		{"responses message", aiFormatOpenAI, "/v1/responses",
			`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"x"}]}]}`, "x"},
		{"responses tool output", aiFormatOpenAI, "/v1/responses",
			`{"model":"m","input":[{"role":"user","content":"x"},{"type":"function_call_output","call_id":"c","output":"y"}]}`, ""},
		{"anthropic cache control", aiFormatAnthropic, "/v1/messages",
			`{"model":"m","max_tokens":5,"messages":[{"role":"user","content":[{"type":"text","text":"q","cache_control":{"type":"ephemeral"}}]}]}`, "q"},
		{"anthropic tool result", aiFormatAnthropic, "/v1/messages",
			`{"model":"m","max_tokens":5,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"r"}]}]}`, ""},
		{"gemini parts", aiFormatGemini, "/v1beta/models/g:generateContent", `{"contents":[{"parts":[{"text":"g1"}]}]}`, "g1"},
		{"gemini inline data", aiFormatGemini, "/v1beta/models/g:generateContent",
			`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`, ""},
		{"embeddings path", aiFormatOpenAI, "/v1/embeddings", `{"model":"m","input":"x"}`, ""},
	} {
		text, masked, err := semanticTurn(tc.format, tc.path, []byte(tc.body))
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: candidate %q, want none", tc.name, text)
			}
			continue
		}
		if err != nil || text != tc.want || !strings.Contains(string(masked), "gregale-semantic-turn") {
			t.Errorf("%s: text=%q masked=%s err=%v", tc.name, text, masked, err)
		}
	}
	// Two calls that differ only in the last user turn share a partition.
	_, first, _ := semanticTurn(aiFormatOpenAI, "/v1/chat/completions", []byte(`{"model":"m","temperature":0.70000000000000001,"messages":[{"role":"user","content":"one"}]}`))
	_, second, _ := semanticTurn(aiFormatOpenAI, "/v1/chat/completions", []byte(`{"messages":[{"role":"user","content":"two"}],"temperature":0.70000000000000001,"model":"m"}`))
	_, other, _ := semanticTurn(aiFormatOpenAI, "/v1/chat/completions", []byte(`{"model":"m","temperature":0.7,"messages":[{"role":"user","content":"one"}]}`))
	if string(first) != string(second) || string(first) == string(other) {
		t.Fatalf("masked bodies: %s / %s / %s", first, second, other)
	}
}

func TestSemanticVector(t *testing.T) {
	vector, err := semanticVector([]byte(`{"data":[{"embedding":[3,4]}]}`))
	if err != nil || len(vector) != 2 || vector[0] != 0.6 || vector[1] != 0.8 {
		t.Fatalf("vector = %v, %v", vector, err)
	}
	for _, body := range []string{`{"data":[]}`, `{"data":[{"embedding":[0,0]}]}`, `not json`} {
		if _, err := semanticVector([]byte(body)); err == nil {
			t.Fatalf("%s: want an error", body)
		}
	}
}

func TestSemanticIndexBounds(t *testing.T) {
	x := newAISemanticIndex()
	x.maxEntries, x.maxBuckets = 2, 2
	x.add("i1", "p", "k1", []float32{1, 0})
	x.add("i1", "p", "k2", []float32{0, 1})
	if key, score, ok := x.nearest("i1", "p", []float32{1, 0}, 0.9); !ok || key != "k1" || score != 1 {
		t.Fatalf("nearest = %q %v %v", key, score, ok)
	}
	if _, _, ok := x.nearest("i1", "other", []float32{1, 0}, 0.9); ok {
		t.Fatal("another partition matched")
	}
	if _, _, ok := x.nearest("i1", "p", []float32{0.7071, 0.7071}, 0.9); ok {
		t.Fatal("a vector below the threshold matched")
	}
	// k1 was used last, so a third entry evicts k2.
	x.add("i1", "p", "k3", []float32{0.6, 0.8})
	if key, _, _ := x.nearest("i1", "p", []float32{0, 1}, 0.5); key != "k3" {
		t.Fatalf("after eviction nearest = %q", key)
	}
	// The same answer replaces its entry.
	x.add("i1", "p", "k3", []float32{0, 1})
	if bucket := x.integrations["i1"]; bucket.entries != 2 || len(bucket.byKey) != 2 {
		t.Fatalf("bucket = %+v", bucket)
	}
	x.add("i2", "p", "k", []float32{1})
	x.add("i3", "p", "k", []float32{1})
	if len(x.integrations) != 2 || x.integrations["i3"] == nil {
		t.Fatalf("integrations = %v", len(x.integrations))
	}
	x.forget("i3", "k")
	if x.integrations["i3"] != nil {
		t.Fatal("forget kept the entry")
	}
	large := &aiSemanticEntry{integrationID: "i2", partition: "p", cacheKey: "k2", vector: make([]float32, 64)}
	large.vector[0] = 1
	x.maxBytes = x.bytes + large.size() - 1
	x.add("i2", "p", "k2", large.vector)
	if key, _, _ := x.nearest("i2", "p", large.vector, 0.9); x.bytes > x.maxBytes || key != "k2" {
		t.Fatalf("bytes %d over %d, nearest %q", x.bytes, x.maxBytes, key)
	}
	x.add("i2", "p", "k3", make([]float32, 1024))
	if x.integrations["i2"].byKey["k3"] != nil {
		t.Fatal("an entry larger than the index was kept")
	}
}

// semanticFixture is one account's chat integration with the semantic
// cache on, embedding through a second integration, in front of a fake
// OpenAI provider whose embeddings are set per input.
type semanticFixture struct {
	handler  *Handler
	backend  *MemoryBackend
	account  string
	app      string
	mu       sync.Mutex
	chats    int
	vectors  map[string][]float64
	embedErr bool
}

func newSemanticFixture(t *testing.T, configure func(*Integration)) *semanticFixture {
	t.Helper()
	f := &semanticFixture{account: uuid.NewString(), app: uuid.NewString(), vectors: map[string][]float64{}}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		_ = json.Unmarshal(body, &request)
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			vector, ok := f.vectors[request.Input]
			if f.embedErr || !ok {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":{"message":"failed"}}`)
				return
			}
			reply, _ := json.Marshal(map[string]any{"object": "list", "model": request.Model,
				"data": []any{map[string]any{"object": "embedding", "index": 0, "embedding": vector}}, "usage": map[string]int{"prompt_tokens": 4, "total_tokens": 4}})
			_, _ = w.Write(reply)
			return
		}
		f.chats++
		reply, _ := json.Marshal(map[string]any{"id": "c1", "object": "chat.completion", "model": request.Model,
			"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "Paris"}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 3}})
		_, _ = w.Write(reply)
	}))
	t.Cleanup(upstream.Close)
	var list []Integration
	for _, id := range []string{"primary", "embedder"} {
		i, err := NewIntegration(id, upstream.URL, "unused", []string{f.app}, 100, 10, 10, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		i.AccountID, i.OwnerKind, i.ProviderAuthMode, i.CredentialSource = f.account, IntegrationOwnerCustomer, ProviderAuthManaged, CredentialSourceCustomerSealed
		i.AccountPlan = api.PlanPro
		i.AllowedMethods, i.AllowedPathPrefixes, i.AIProvider = []string{http.MethodPost}, []string{"/v1"}, api.OutboundAIProviderOpenAI
		if id == "primary" {
			i.ResponseCacheTTLSeconds = 60
			i.AISemanticCache = &api.OutboundAISemanticCache{Enabled: true, EmbeddingIntegrationID: "embedder", EmbeddingModel: "embed-1"}
			if configure != nil {
				configure(&i)
			}
		}
		list = append(list, i)
	}
	resolver, err := NewStaticResolver(list)
	if err != nil {
		t.Fatal(err)
	}
	f.backend = NewMemoryBackend()
	if f.handler, err = NewHandler(resolver, f.backend, upstream.Client()); err != nil {
		t.Fatal(err)
	}
	f.handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	return f
}

func (f *semanticFixture) ask(question, endUser string) *httptest.ResponseRecorder {
	body := `{"model":"gpt-current","messages":[{"role":"system","content":"geography"},{"role":"user","content":"` + question + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774/i/primary/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if endUser != "" {
		req.Header.Set(api.OutboundAIUserHeader, endUser)
	}
	req = req.WithContext(withRelayIdentity(context.Background(), WorkloadIdentity{AccountID: f.account, AppID: f.app, InstanceID: "i-1"}))
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, req)
	return recorder
}

func (f *semanticFixture) chatCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chats
}

func TestSemanticCacheAnswersSimilarCalls(t *testing.T) {
	f := newSemanticFixture(t, nil)
	f.vectors["capital of France?"] = []float64{1, 0, 0}
	f.vectors["France's capital?"] = []float64{0.99, 0.1, 0}
	f.vectors["capital of Spain?"] = []float64{0.6, 0.8, 0}
	if resp := f.ask("capital of France?", "u1"); resp.Code != http.StatusOK || resp.Header().Get(AICacheHeader) != "" || f.chatCalls() != 1 {
		t.Fatalf("first call = %d cache=%q chats=%d", resp.Code, resp.Header().Get(AICacheHeader), f.chatCalls())
	}
	resp := f.ask("France's capital?", "u1")
	if resp.Code != http.StatusOK || resp.Header().Get(AICacheHeader) != "semantic" || resp.Header().Get(AISimilarityHeader) != "0.9949" ||
		!strings.Contains(resp.Body.String(), "Paris") || f.chatCalls() != 1 {
		t.Fatalf("similar call = %d cache=%q similarity=%q chats=%d body=%s", resp.Code, resp.Header().Get(AICacheHeader),
			resp.Header().Get(AISimilarityHeader), f.chatCalls(), resp.Body.String())
	}
	if row, ok := f.backend.AIUsageRollup("primary", f.app, "gpt-current"); !ok || row.Requests != 2 || row.CachedRequests != 1 {
		t.Fatalf("primary usage = %+v, %v", row, ok)
	}
	if resp := f.ask("capital of Spain?", "u1"); resp.Header().Get(AICacheHeader) != "" || f.chatCalls() != 2 {
		t.Fatalf("dissimilar call cache=%q chats=%d", resp.Header().Get(AICacheHeader), f.chatCalls())
	}
	// Another end user has their own answers.
	if resp := f.ask("France's capital?", "u2"); resp.Header().Get(AICacheHeader) != "" || f.chatCalls() != 3 {
		t.Fatalf("other end user cache=%q chats=%d", resp.Header().Get(AICacheHeader), f.chatCalls())
	}
	if row, ok := f.backend.AIUsageRollup("embedder", AIUsageCallerSemanticCache, "embed-1"); !ok || row.Requests != 4 {
		t.Fatalf("embedding usage = %+v, %v", row, ok)
	}
}

func TestSemanticCacheSharedAcrossUsersAndFailOpen(t *testing.T) {
	f := newSemanticFixture(t, func(i *Integration) { i.AISemanticCache.ShareAcrossUsers = true })
	f.vectors["capital of France?"] = []float64{1, 0, 0}
	f.vectors["France's capital?"] = []float64{0.99, 0.1, 0}
	f.ask("capital of France?", "u1")
	if resp := f.ask("France's capital?", "u2"); resp.Header().Get(AICacheHeader) != "semantic" || f.chatCalls() != 1 {
		t.Fatalf("shared call cache=%q chats=%d", resp.Header().Get(AICacheHeader), f.chatCalls())
	}
	f.embedErr = true
	if resp := f.ask("France's capital?", "u3"); resp.Code != http.StatusOK || resp.Header().Get(AICacheHeader) != "" || f.chatCalls() != 2 {
		t.Fatalf("failed embedding = %d cache=%q chats=%d", resp.Code, resp.Header().Get(AICacheHeader), f.chatCalls())
	}
}

func TestResolvedSemanticCache(t *testing.T) {
	raw := []byte(`{"enabled":true,"embedding_integration_id":"e","embedding_model":"m"}`)
	if got := resolvedSemanticCache(raw, api.PlanPro, api.OutboundAIProviderOpenAI, 60); got == nil || got.Threshold() != 9500 {
		t.Fatalf("pro = %+v", got)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
		plan api.Plan
		ttl  int
	}{
		{"hobby", raw, api.PlanHobby, 60},
		{"no response cache", raw, api.PlanPro, 0},
		{"disabled", []byte(`{"enabled":false,"embedding_integration_id":"e","embedding_model":"m"}`), api.PlanPro, 60},
		{"unreadable", []byte(`[]`), api.PlanPro, 60},
	} {
		if got := resolvedSemanticCache(tc.raw, tc.plan, api.OutboundAIProviderOpenAI, tc.ttl); got != nil {
			t.Errorf("%s = %+v, want off", tc.name, got)
		}
	}
}
