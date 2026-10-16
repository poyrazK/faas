package outbound

// adr: 997 — the replay worker resends a captured call to each target
// through the ordinary path, translated where needed, and records usage,
// cost, latency and output text under the "replay" caller.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeReplayStore struct {
	jobs     []AIReplayJob
	finished []AIReplayResult
	source   string
	code     string
}

func (s *fakeReplayStore) ClaimAIReplay(context.Context) (AIReplayJob, bool, error) {
	if len(s.jobs) == 0 {
		return AIReplayJob{}, false, nil
	}
	job := s.jobs[0]
	s.jobs = s.jobs[1:]
	return job, true, nil
}

func (s *fakeReplayStore) FinishAIReplay(_ context.Context, _ AIReplayJob, source string, results []AIReplayResult, code string) error {
	s.finished, s.source, s.code = results, source, code
	return nil
}

func TestReplayWorkerComparesTargets(t *testing.T) {
	var received []string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = append(received, r.URL.Path+" "+string(body))
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5",` +
				`"content":[{"type":"text","text":"Bonjour from Claude, a@example.com"}],"stop_reason":"end_turn",` +
				`"usage":{"input_tokens":12,"output_tokens":5}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,` +
			`"message":{"role":"assistant","content":"Hello from GPT"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	account := uuid.NewString()
	integration := func(id, provider, accountID string) Integration {
		i, err := NewIntegration(id, upstream.URL, "unused", nil, 100, 10, 10, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		i.AccountID, i.OwnerKind, i.ProviderAuthMode, i.CredentialSource = accountID, IntegrationOwnerCustomer, ProviderAuthManaged, CredentialSourceCustomerSealed
		i.AccountPlan = api.PlanPro
		i.AllowedMethods, i.AllowedPathPrefixes, i.AIProvider = []string{http.MethodPost}, []string{"/v1"}, provider
		return i
	}
	openai, anthropic := integration("openai", api.OutboundAIProviderOpenAI, account), integration("anthropic", api.OutboundAIProviderAnthropic, account)
	foreign := integration("foreign", api.OutboundAIProviderOpenAI, uuid.NewString())
	price := int64(1_000_000)
	anthropic.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "claude", InputMillicentsPerMTok: price, OutputMillicentsPerMTok: price}})
	resolver, err := NewStaticResolver([]Integration{openai, anthropic, foreign})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler.AIGatewayEnabled = true
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	store := &fakeReplayStore{jobs: []AIReplayJob{{
		ID: uuid.NewString(), AccountID: account, IntegrationID: "openai", SourceModel: "gpt-4o",
		Targets: []AIRouteTarget{{IntegrationID: "anthropic", Model: "claude-sonnet-4-5"}, {IntegrationID: "openai", Model: "gpt-4o-mini"},
			{IntegrationID: "foreign"}, {IntegrationID: "missing"}},
		RequestPath: "/v1/chat/completions",
		RequestBody: []byte(`{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Say hello"}]}`),
		ResponseBody: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hi \"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"there\"}}]}\n\ndata: [DONE]\n\n"),
		ResponseContentType: "text/event-stream",
	}}}
	worker := &AIReplayWorker{Store: store, Handler: handler}
	if processed, err := worker.ProcessOne(context.Background()); !processed || err != nil {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if store.source != "Hi there" || store.code != "" || len(store.finished) != 4 {
		t.Fatalf("finished: source %q, code %q, %d results", store.source, store.code, len(store.finished))
	}
	claude, gpt, other, missing := store.finished[0], store.finished[1], store.finished[2], store.finished[3]
	if claude.Status != http.StatusOK || claude.Outcome != AIUsageOutcomeKnown || claude.Usage.InputTokens != 12 ||
		claude.CostMillicents != 17 || claude.OutputText != "Bonjour from Claude, [REDACTED:email]" || claude.Model != "claude-sonnet-4-5" ||
		!json.Valid(claude.ResponseBody) || !strings.Contains(string(claude.ResponseBody), `"chat.completion"`) {
		t.Fatalf("claude result = %+v", claude)
	}
	if gpt.Status != http.StatusOK || gpt.OutputText != "Hello from GPT" || gpt.Usage.OutputTokens != 3 || gpt.TTFBMS == nil {
		t.Fatalf("gpt result = %+v", gpt)
	}
	if other.ErrorCode != "outbound_replay_target_unavailable" || missing.ErrorCode != "outbound_replay_target_unavailable" || other.Status != 0 {
		t.Fatalf("unavailable targets = %+v / %+v", other, missing)
	}
	for _, call := range received {
		if strings.Contains(call, `"stream"`) || strings.Contains(call, "stream_options") {
			t.Fatalf("a replayed call streamed: %s", call)
		}
	}
	if !strings.HasPrefix(received[0], "/v1/messages ") || !strings.Contains(received[1], `"model":"gpt-4o-mini"`) {
		t.Fatalf("upstream calls = %q", received)
	}
	if row, ok := backend.AIUsageRollup("anthropic", AIUsageCallerReplay, "claude-sonnet-4-5"); !ok || row.Requests != 1 {
		t.Fatalf("replay rollup = %+v, %v", row, ok)
	}
	if processed, err := worker.ProcessOne(context.Background()); processed || err != nil {
		t.Fatalf("an empty queue = %v, %v", processed, err)
	}
}

func TestBuildReplayAttempt(t *testing.T) {
	gemini := Integration{AIProvider: api.OutboundAIProviderGemini}
	anthropic := Integration{AIProvider: api.OutboundAIProviderAnthropic}
	openai := Integration{AIProvider: api.OutboundAIProviderOpenAI}
	stream := AIReplayJob{SourceModel: "gemini-2.5-flash", RequestPath: "/v1beta/models/gemini-2.5-flash:streamGenerateContent",
		RequestBody: []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`)}
	attempt, err := buildReplayAttempt(stream, gemini, gemini, "gemini-2.5-pro")
	if err != nil || attempt.path != "/v1beta/models/gemini-2.5-pro:generateContent" || attempt.model != "gemini-2.5-pro" {
		t.Fatalf("gemini attempt = %+v, %v", attempt, err)
	}
	if _, err := buildReplayAttempt(stream, gemini, anthropic, "claude-sonnet-4-5"); !errors.Is(err, errReplayUnsupported) {
		t.Fatalf("a native capture on another provider = %v", err)
	}
	messages := AIReplayJob{SourceModel: "claude-sonnet-4-5", RequestPath: "/v1/messages",
		RequestBody: []byte(`{"model":"claude-sonnet-4-5","max_tokens":10,"stream":true,"messages":[]}`)}
	attempt, err = buildReplayAttempt(messages, anthropic, anthropic, "claude-haiku-4-5")
	if err != nil || attempt.path != "/v1/messages" || !strings.Contains(string(attempt.body), `"model":"claude-haiku-4-5"`) ||
		strings.Contains(string(attempt.body), "stream") {
		t.Fatalf("anthropic attempt = %+v, %v", attempt, err)
	}
	if attempt, err := buildReplayAttempt(messages, anthropic, anthropic, ""); err != nil || attempt.model != "claude-sonnet-4-5" ||
		!strings.Contains(string(attempt.body), `"model":"claude-sonnet-4-5"`) {
		t.Fatalf("an attempt keeping the model = %+v, %v", attempt, err)
	}
	if _, err := buildReplayAttempt(AIReplayJob{RequestPath: "/v1/chat/completions", RequestBody: []byte("not json")}, openai, openai, ""); !errors.Is(err, errReplayUnsupported) {
		t.Fatalf("a non-JSON capture = %v", err)
	}
	responses := AIReplayJob{SourceModel: "gpt-5", RequestPath: "/v1/responses", RequestBody: []byte(`{"model":"gpt-5","input":"hi","stream":true}`)}
	attempt, err = buildReplayAttempt(responses, openai, anthropic, "claude-sonnet-4-5")
	if err != nil || attempt.path != "/v1/messages" || attempt.translator == nil {
		t.Fatalf("a Responses capture on Anthropic = %+v, %v", attempt, err)
	}
}

func TestExtractOutputText(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, want string
	}{
		{"chat", "application/json", `{"choices":[{"message":{"content":"hi","tool_calls":[{"function":{"name":"f","arguments":"{}"}}]}}]}`, "hi[f({})]"},
		{"chat stream", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\ndata: [DONE]\n", "ab"},
		{"anthropic", "application/json", `{"content":[{"type":"text","text":"x"},{"type":"tool_use","name":"t"}]}`, "x[t]"},
		{"anthropic stream", "text/event-stream", "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"yo\"}}\n\n", "yo"},
		{"gemini", "application/json", `{"candidates":[{"content":{"parts":[{"text":"g1"},{"text":"g2"}]}}]}`, "g1g2"},
		{"responses", "application/json", `{"output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"r"}]},{"type":"function_call","name":"n","arguments":"{}"}]}`, "r[n({})]"},
		{"responses stream", "text/event-stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"d\"}\n\n", "d"},
		{"converse", "application/json", `{"output":{"message":{"content":[{"text":"c"}]}}}`, "c"},
		{"embeddings", "application/json", `{"data":[{"embedding":[0.1]},{"embedding":[0.2]}]}`, "[2 embeddings]"},
		{"transcription", "application/json", `{"text":"spoken"}`, "spoken"},
		{"binary", "audio/mpeg", "ID3", ""},
	} {
		if got := extractOutputText([]byte(tc.body), tc.contentType); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := truncateUTF8("héllo", 2); got != "h" {
		t.Fatalf("truncateUTF8 cut a character: %q", got)
	}
}

func TestReplayPrincipalIsLimitedToTheAccount(t *testing.T) {
	integration := Integration{AccountID: "a", OwnerKind: IntegrationOwnerCustomer, ProviderAuthMode: ProviderAuthManaged, AIProvider: api.OutboundAIProviderOpenAI}
	if !replayAllowed(integration, replayPrincipal{accountID: "a"}) || replayAllowed(integration, replayPrincipal{accountID: "b"}) {
		t.Fatal("replay account check")
	}
	integration.AIProvider = ""
	if replayAllowed(integration, replayPrincipal{accountID: "a"}) {
		t.Fatal("a replay reached a non-AI integration")
	}
	if _, ok := replayPrincipalFromContext(context.Background()); ok {
		t.Fatal("a context without a replay principal had one")
	}
}
