package outbound

// adr: 1431 — batches reserve their estimate before the provider call and
// settle actual usage once the provider ends them; access tokens act as
// their app on the public listener; evaluations judge a candidate target
// against the first target's captured calls.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func phase9Integration(t *testing.T, id, origin, provider, account string, apps ...string) Integration {
	t.Helper()
	integration, err := NewIntegration(id, origin, "unused", apps, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AccountID, integration.OwnerKind, integration.ProviderAuthMode = account, IntegrationOwnerCustomer, ProviderAuthManaged
	integration.CredentialSource, integration.AccountPlan = CredentialSourceCustomerSealed, api.PlanPro
	integration.AllowedMethods, integration.AllowedPathPrefixes = []string{http.MethodGet, http.MethodPost}, []string{"/v1"}
	integration.AIProvider = provider
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "gpt", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 2_000_000}})
	return integration
}

func phase9Handler(t *testing.T, client *http.Client, integrations ...Integration) (*Handler, *MemoryBackend) {
	t.Helper()
	resolver, err := NewStaticResolver(integrations)
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, client)
	if err != nil {
		t.Fatal(err)
	}
	handler.AIGatewayEnabled = true
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	return handler, backend
}

// asApp sends a request as an app through an access token principal.
func asApp(handler http.Handler, method, path, body string, token AIAccessToken) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(withAccessToken(request.Context(), token))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAIBatchReservesThenSettlesActualUsage(t *testing.T) {
	var completed atomic.Bool
	var creations atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-secret" {
			http.Error(w, "no provider key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/files/file-in/content":
			line := `{"custom_id":"%d","method":"POST","url":"/v1/chat/completions","body":{"model":"gpt-4o-mini","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}}`
			_, _ = io.WriteString(w, strings.ReplaceAll(line, "%d", "1")+"\n"+strings.ReplaceAll(line, "%d", "2")+"\n")
		case "POST /v1/batches":
			creations.Add(1)
			_, _ = io.WriteString(w, `{"id":"batch_abc","object":"batch","status":"validating"}`)
		case "GET /v1/batches/batch_abc":
			if !completed.Load() {
				_, _ = io.WriteString(w, `{"id":"batch_abc","status":"in_progress","output_file_id":null}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"batch_abc","status":"completed","output_file_id":"file-out"}`)
		case "GET /v1/files/file-out/content":
			result := `{"id":"r","custom_id":"x","response":{"status_code":200,"body":{"id":"c","object":"chat.completion","model":"gpt-4o-mini",` +
				`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}}}`
			failed := `{"id":"r2","custom_id":"y","response":{"status_code":500,"body":{"error":{"message":"boom"}}}}`
			_, _ = io.WriteString(w, result+"\n"+result+"\n"+failed+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "openai", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	handler, backend := phase9Handler(t, upstream.Client(), integration)
	now := time.Now()
	backend.SetClock(func() time.Time { return now })
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: now.Add(time.Hour)}

	created := asApp(handler, http.MethodPost, "/i/openai/v1/batches",
		`{"input_file_id":"file-in","endpoint":"/v1/chat/completions","completion_window":"24h"}`, token)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), "batch_abc") {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	batches := backend.AIBatches("openai")
	if len(batches) != 1 || batches[0].ProviderBatchID != "batch_abc" || batches[0].Requests != 2 || batches[0].Caller != app ||
		batches[0].Model != "gpt-4o-mini" || batches[0].ReservedTokens < 200 || batches[0].ReservedCostMillicents <= 0 {
		t.Fatalf("reserved batches = %+v", batches)
	}

	worker := &AIBatchWorker{Backend: backend, Usage: backend, Handler: handler}
	poll := api.OutboundAIBatchPollIntervalSeconds * time.Second
	// The first check waits one poll interval; the provider has not finished.
	for _, wait := range []time.Duration{0, poll} {
		now = now.Add(wait)
		if settled, err := worker.CheckOnce(context.Background()); settled != 0 || err != nil {
			t.Fatalf("an unfinished batch settled: %d, %v", settled, err)
		}
	}
	if _, open := backend.AIBatchOpen("openai", batches[0].ID); !open {
		t.Fatal("an unfinished batch lost its reservation")
	}
	completed.Store(true)
	if settled, err := worker.CheckOnce(context.Background()); settled != 0 || err != nil {
		t.Fatalf("a batch was checked again before its poll interval: %d, %v", settled, err)
	}
	now = now.Add(poll)
	if settled, err := worker.CheckOnce(context.Background()); settled != 1 || err != nil {
		t.Fatalf("CheckOnce = %d, %v", settled, err)
	}
	if _, open := backend.AIBatchOpen("openai", batches[0].ID); open {
		t.Fatal("a settled batch kept its reservation")
	}
	row, ok := backend.AIUsageRollup("openai", app, "gpt-4o-mini")
	listCost := aiCallCostMillicents(integration, AIUsage{Model: "gpt-4o-mini", Known: true, InputTokens: 20, OutputTokens: 10})
	if !ok || row.Requests != 1 || row.InputTokens != 20 || row.OutputTokens != 10 || row.CostMillicents != batchCost(listCost) ||
		batchCost(listCost) >= listCost {
		t.Fatalf("settled usage = %+v, %v (list cost %d)", row, ok, listCost)
	}
	if settled, err := worker.CheckOnce(context.Background()); settled != 0 || err != nil || creations.Load() != 1 {
		t.Fatalf("a settled batch was checked again: %d, %v", settled, err)
	}
}

func TestAIBatchRefusals(t *testing.T) {
	var creations atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/files/big/content":
			line := `{"custom_id":"1","method":"POST","url":"/v1/chat/completions","body":{"model":"gpt-4o","max_tokens":4000,"messages":[]}}`
			_, _ = io.WriteString(w, line+"\n"+line+"\n")
		case "POST /v1/batches", "POST /v1/messages/batches":
			creations.Add(1)
			_, _ = io.WriteString(w, `{"id":"batch_1","status":"validating"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	capped := phase9Integration(t, "capped", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	limit := int64(1000)
	capped.DailyAITokenLimit = &limit
	anthropic := phase9Integration(t, "anthropic", upstream.URL, api.OutboundAIProviderAnthropic, account, app)
	handler, backend := phase9Handler(t, upstream.Client(), capped, anthropic)
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}

	if got := asApp(handler, http.MethodPost, "/i/capped/v1/batches", `{"input_file_id":"big","endpoint":"/v1/chat/completions"}`, token); got.Code != http.StatusTooManyRequests {
		t.Fatalf("a batch past the daily cap = %d %s", got.Code, got.Body.String())
	}
	if got := asApp(handler, http.MethodPost, "/i/capped/v1/batches", `{"input_file_id":"missing","endpoint":"/v1/chat/completions"}`, token); got.Code != http.StatusBadRequest ||
		!strings.Contains(got.Body.String(), "outbound_ai_batch_unreadable") {
		t.Fatalf("an unreadable input file = %d %s", got.Code, got.Body.String())
	}
	if got := asApp(handler, http.MethodPost, "/i/anthropic/v1/messages/batches", `{"requests":[]}`, token); got.Code != http.StatusBadRequest {
		t.Fatalf("an empty Anthropic batch = %d %s", got.Code, got.Body.String())
	}
	if creations.Load() != 0 || len(backend.AIBatches("capped")) != 0 {
		t.Fatalf("a refused batch reached the provider (%d) or kept a reservation", creations.Load())
	}
	body := `{"requests":[{"custom_id":"a","params":{"model":"claude-haiku-4-5","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}}]}`
	if got := asApp(handler, http.MethodPost, "/i/anthropic/v1/messages/batches", body, token); got.Code != http.StatusOK {
		t.Fatalf("an Anthropic batch = %d %s", got.Code, got.Body.String())
	}
	if batches := backend.AIBatches("anthropic"); len(batches) != 1 || batches[0].Requests != 1 || batches[0].Model != "claude-haiku-4-5" ||
		batches[0].Path != "/v1/messages/batches" {
		t.Fatalf("anthropic batches = %+v", batches)
	}
}

func TestEstimateBatchInputs(t *testing.T) {
	integration := Integration{}
	estimate, ok := estimateAnthropicBatch(integration, []byte(`{"requests":[{"params":{"model":"m","max_tokens":10}},{"params":{"model":"m"}}]}`))
	if !ok || estimate.requests != 2 || estimate.model() != "m" ||
		estimate.tokens <= 10+api.OutboundAIDefaultOutputReserveTokens {
		t.Fatalf("anthropic estimate = %+v, %v", estimate, ok)
	}
	if _, ok := estimateAnthropicBatch(integration, []byte(`{"requests":[{"params":"x"}]}`)); ok {
		t.Fatal("an invalid Anthropic request was sized")
	}
	lines := &batchLineEstimator{}
	input := `{"url":"/v1/embeddings","body":{"model":"e","input":"a"}}` + "\n\n" +
		`{"url":"/v1/chat/completions","body":{"model":"c","max_completion_tokens":7}}`
	for _, chunk := range []string{input[:13], input[13:40], input[40:]} {
		if _, err := lines.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	estimate, ok = lines.finish()
	if !ok || estimate.requests != 2 || estimate.models["e"] != 1 || estimate.models["c"] != 1 {
		t.Fatalf("line estimate = %+v, %v", estimate, ok)
	}
	invalid := &batchLineEstimator{}
	_, _ = invalid.Write([]byte("not json\n"))
	if _, ok := invalid.finish(); ok {
		t.Fatal("an invalid input line was sized")
	}
	long := &batchLineEstimator{}
	_, _ = long.Write([]byte(strings.Repeat("x", api.OutboundAIBatchLineMaxBytes+10) + "\n"))
	if estimate, ok := long.finish(); !ok || estimate.requests != 1 || estimate.tokens < int64(api.OutboundAIBatchLineMaxBytes)/api.OutboundAIReserveBytesPerToken {
		t.Fatalf("a long line = %+v, %v", estimate, ok)
	}
	if batchSettlementKey("b", "m1") == batchSettlementKey("b", "m2") || batchSettlementKey("b", "m1") != batchSettlementKey("b", "m1") {
		t.Fatal("settlement keys are not one per batch and model")
	}
	if batchProviderID([]byte(`{"id":"../x"}`)) != "" || batchProviderID([]byte(`{"id":"msgbatch_01"}`)) != "msgbatch_01" {
		t.Fatal("batchProviderID")
	}
}

type countingTokens struct {
	StaticAccessTokens
	lookups atomic.Int32
}

func (c *countingTokens) AIAccessToken(ctx context.Context, hash [32]byte) (AIAccessToken, error) {
	c.lookups.Add(1)
	return c.StaticAccessTokens.AIAccessToken(ctx, hash)
}

func TestPublicHandlerServesAccessTokensAsTheirApp(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization")+"|"+r.Header.Get("X-Api-Key")+"|"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,`+
			`"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	openai := phase9Integration(t, "openai", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	other := phase9Integration(t, "other", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	handler, backend := phase9Handler(t, upstream.Client(), openai, other)
	secret := func(c byte) string { return api.OutboundAIAccessTokenPrefix + strings.Repeat(string(c), 43) }
	now := time.Now()
	tokens := &countingTokens{StaticAccessTokens: StaticAccessTokens{
		secret('a'): {ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: now.Add(time.Hour)},
		secret('b'): {ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: now.Add(time.Hour),
			IntegrationIDs: map[string]struct{}{"other": {}}},
		secret('c'): {ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: now.Add(-time.Minute)},
	}}
	public := &PublicHandler{Handler: handler, Tokens: tokens, Now: func() time.Time { return now }}
	send := func(path string, header http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`))
		for name, values := range header {
			request.Header[name] = values
		}
		recorder := httptest.NewRecorder()
		public.ServeHTTP(recorder, request)
		return recorder
	}
	bearer := func(token string) http.Header { return http.Header{"Authorization": {"Bearer " + token}} }
	for _, tc := range []struct {
		name, path string
		header     http.Header
		status     int
		code       string
	}{
		{"control path", "/readyz", bearer(secret('a')), http.StatusNotFound, "outbound_integration_not_found"},
		{"no token", "/i/openai/v1/chat/completions", nil, http.StatusUnauthorized, "outbound_access_token_invalid"},
		{"not a token", "/i/openai/v1/chat/completions", bearer("sk-provider-key"), http.StatusUnauthorized, "outbound_access_token_invalid"},
		{"unknown token", "/i/openai/v1/chat/completions", bearer(secret('z')), http.StatusUnauthorized, "outbound_access_token_invalid"},
		{"expired token", "/i/openai/v1/chat/completions", bearer(secret('c')), http.StatusUnauthorized, "outbound_access_token_invalid"},
		{"narrowed token", "/i/openai/v1/chat/completions", bearer(secret('b')), http.StatusForbidden, "outbound_access_token_not_allowed"},
	} {
		got := send(tc.path, tc.header)
		if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.code) {
			t.Errorf("%s = %d %s", tc.name, got.Code, got.Body.String())
		}
	}
	lookups := tokens.lookups.Load()
	if got := send("/i/openai/v1/chat/completions", bearer(secret('z'))); got.Code != http.StatusUnauthorized || tokens.lookups.Load() != lookups {
		t.Fatalf("an unknown token was looked up again within the cache (%d lookups)", tokens.lookups.Load())
	}
	header := http.Header{"X-Api-Key": {secret('a')}, WorkloadIdentityHeader: {"forged"}, TokenHeader: {"forged"}}
	if got := send("/i/openai/v1/chat/completions?key=leak&api-version=1", header); got.Code != http.StatusOK {
		t.Fatalf("an SDK key header = %d %s", got.Code, got.Body.String())
	}
	if got := send("/i/other/v1/chat/completions", bearer(secret('b'))); got.Code != http.StatusOK {
		t.Fatalf("a narrowed token on its integration = %d %s", got.Code, got.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "Bearer provider-secret||api-version=1" {
		t.Fatalf("provider saw %q", seen)
	}
	if row, ok := backend.AIUsageRollup("openai", app, "gpt-4o-mini"); !ok || row.Requests != 1 {
		t.Fatalf("token usage = %+v, %v", row, ok)
	}
}

func TestAccessTokenShapeAndQueryKey(t *testing.T) {
	good := api.OutboundAIAccessTokenPrefix + strings.Repeat("A", 42) + "-"
	if !validAccessTokenShape(good) || validAccessTokenShape(good+"x") || validAccessTokenShape(strings.Replace(good, "A", "+", 1)) ||
		validAccessTokenShape("sk_"+good[5:]) {
		t.Fatal("validAccessTokenShape")
	}
	if got := withoutQueryKey("key=a&b=2&keys=3&key", "key"); got != "b=2&keys=3" {
		t.Fatalf("withoutQueryKey = %q", got)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Basic abc")
	request.Header.Set("Api-Key", "azure-style")
	if got := accessTokenFromRequest(request); got != "azure-style" {
		t.Fatalf("a non-bearer Authorization hid the next header: %q", got)
	}
	hash := sha256.Sum256([]byte(good))
	store := StaticAccessTokens{good: {ID: "t", ExpiresAt: time.Now().Add(time.Hour)}}
	if token, err := store.AIAccessToken(context.Background(), hash); err != nil || token.ID != "t" {
		t.Fatalf("static lookup = %+v, %v", token, err)
	}
}

func TestEvaluationWorkerJudgesTheCandidate(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply := func(text string) {
			w.Header().Set("Content-Type", "application/json")
			encoded, _ := json.Marshal(text)
			_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","model":"`+body.Model+`","choices":[{"index":0,`+
				`"message":{"role":"assistant","content":`+string(encoded)+`}}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`)
		}
		last := body.Messages[len(body.Messages)-1].Content
		if body.Model == "judge-model" {
			answerA := last[strings.Index(last, "<<<ANSWER A>>>"):strings.Index(last, "<<<ANSWER B>>>")]
			candidate := "B"
			if strings.Contains(answerA, "candidate") {
				candidate = "A"
			}
			baseline := map[string]string{"A": "B", "B": "A"}[candidate]
			switch {
			case strings.Contains(last, "q-win"):
				reply(`Verdict: {"winner": "` + candidate + `"}`)
			case strings.Contains(last, "q-loss"):
				reply(`{"winner":"` + strings.ToLower(baseline) + `"}`)
			case strings.Contains(last, "q-tie"):
				reply(`{"winner":"tie"}`)
			default:
				reply("I cannot decide")
			}
			return
		}
		if strings.Contains(last, "q-fail") {
			http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
			return
		}
		reply("candidate answer to " + last)
	}))
	defer upstream.Close()
	account := uuid.NewString()
	baseline := phase9Integration(t, "baseline", upstream.URL, api.OutboundAIProviderOpenAI, account)
	candidate := phase9Integration(t, "candidate", upstream.URL, api.OutboundAIProviderOpenAI, account)
	judge := phase9Integration(t, "judge", upstream.URL, api.OutboundAIProviderOpenAI, account)
	anthropic := phase9Integration(t, "anthropic", upstream.URL, api.OutboundAIProviderAnthropic, account)
	handler, backend := phase9Handler(t, upstream.Client(), baseline, candidate, judge, anthropic)
	sample := func(question string) AIEvaluationSample {
		ttfb := int64(40)
		return AIEvaluationSample{LogID: uuid.NewString(), SourceModel: "gpt-4o", RequestPath: "/v1/chat/completions",
			RequestBody:         []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"` + question + `"}]}`),
			ResponseBody:        []byte(`{"choices":[{"message":{"content":"baseline answer, mail a@example.com"}}]}`),
			ResponseContentType: "application/json", CostMillicents: 9, TTFBMS: &ttfb}
	}
	job := AIEvaluationJob{ID: uuid.NewString(), AccountID: account, RouteID: uuid.NewString(), Position: 2,
		Baseline: AIRouteTarget{IntegrationID: "baseline"}, Candidate: AIRouteTarget{IntegrationID: "candidate", Model: "gpt-4o-mini"},
		JudgeIntegrationID: "judge", JudgeModel: "judge-model", MaxLossRate: 40,
		Samples: []AIEvaluationSample{sample("q-win"), sample("q-loss"), sample("q-tie"), sample("q-unsure"), sample("q-fail")}}
	store := &fakeEvaluationStore{jobs: []AIEvaluationJob{job}}
	var flips atomic.Int32
	worker := &AIEvaluationWorker{Store: store, Handler: handler, CandidateFirst: func() bool { return flips.Add(1)%2 == 0 }}
	if processed, err := worker.ProcessOne(context.Background()); !processed || err != nil {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if store.code != "" || len(store.items) != 5 {
		t.Fatalf("finished with code %q and %d items", store.code, len(store.items))
	}
	want := []string{api.OutboundAIEvaluationItemWin, api.OutboundAIEvaluationItemLoss, api.OutboundAIEvaluationItemTie,
		api.OutboundAIEvaluationItemJudgeError, api.OutboundAIEvaluationItemCandidateError}
	for index, item := range store.items {
		if item.Verdict != want[index] || item.LogID != job.Samples[index].LogID || item.BaselineCostMillicents != 9 {
			t.Errorf("item %d = %+v, want %s", index, item, want[index])
		}
	}
	win := store.items[0]
	if win.CandidateStatus != http.StatusOK || win.CandidateCostMillicents <= 0 || win.CandidateTTFBMS == nil ||
		!strings.Contains(win.CandidateOutputText, "candidate answer to q-win") ||
		win.BaselineOutputText != "baseline answer, mail [REDACTED:email]" {
		t.Fatalf("win item = %+v", win)
	}
	if failed := store.items[4]; failed.CandidateStatus != http.StatusServiceUnavailable || failed.CandidateOutputText != "" {
		t.Fatalf("candidate error item = %+v", failed)
	}
	verdict := JudgeAIEvaluation(store.items, job.MaxLossRate)
	if !verdict.Pass || verdict.Wins != 1 || verdict.Losses != 1 || verdict.Ties != 1 || verdict.JudgeErrors != 1 || verdict.CandidateErrors != 1 {
		t.Fatalf("verdict = %+v", verdict)
	}
	if JudgeAIEvaluation(store.items, 39).Pass {
		t.Fatal("two losses of five passed a 39% threshold")
	}
	if row, ok := backend.AIUsageRollup("candidate", AIUsageCallerEvaluation, "gpt-4o-mini"); !ok || row.Requests != 5 {
		t.Fatalf("candidate usage = %+v, %v", row, ok)
	}
	if row, ok := backend.AIUsageRollup("judge", AIUsageCallerEvaluation, "judge-model"); !ok || row.Requests != 4 {
		t.Fatalf("judge usage = %+v, %v", row, ok)
	}

	for _, tc := range []struct {
		name string
		edit func(*AIEvaluationJob)
		code string
	}{
		{"no captures", func(j *AIEvaluationJob) { j.Samples = nil }, "outbound_ai_evaluation_no_captures"},
		{"native baseline", func(j *AIEvaluationJob) { j.Baseline.IntegrationID = "anthropic" }, "outbound_ai_evaluation_unsupported"},
		{"missing candidate", func(j *AIEvaluationJob) { j.Candidate.IntegrationID = "missing" }, "outbound_ai_evaluation_target_unavailable"},
		{"foreign account", func(j *AIEvaluationJob) { j.AccountID = uuid.NewString() }, "outbound_ai_evaluation_unsupported"},
		{"silent judge", func(j *AIEvaluationJob) {
			j.Samples = []AIEvaluationSample{sample("q-unsure"), sample("q-unsure"), sample("q-win")}
		}, "outbound_ai_evaluation_judge_failed"},
	} {
		failing := job
		failing.ID = uuid.NewString()
		tc.edit(&failing)
		store := &fakeEvaluationStore{jobs: []AIEvaluationJob{failing}}
		worker := &AIEvaluationWorker{Store: store, Handler: handler}
		if processed, err := worker.ProcessOne(context.Background()); !processed || err != nil || store.code != tc.code {
			t.Errorf("%s = %v, %v, code %q", tc.name, processed, err, store.code)
		}
	}
}

type fakeEvaluationStore struct {
	jobs  []AIEvaluationJob
	items []AIEvaluationItem
	code  string
}

func (s *fakeEvaluationStore) ClaimAIEvaluation(context.Context) (AIEvaluationJob, bool, error) {
	if len(s.jobs) == 0 {
		return AIEvaluationJob{}, false, nil
	}
	job := s.jobs[0]
	s.jobs = s.jobs[1:]
	return job, true, nil
}

func (s *fakeEvaluationStore) FinishAIEvaluation(_ context.Context, job AIEvaluationJob, items []AIEvaluationItem, code string) (AIEvaluationVerdict, error) {
	s.items, s.code = items, code
	return JudgeAIEvaluation(items, job.MaxLossRate), nil
}

func TestJudgeWinner(t *testing.T) {
	for _, tc := range []struct {
		text, want string
		ok         bool
	}{
		{`{"winner": "A"}`, "A", true},
		{"Thinking... {\"winner\":\"b\"} done", "B", true},
		{`{"winner":"TIE"}`, "tie", true},
		{`{"winner":"C"}`, "", false},
		{`{"winner":}`, "", false},
		{"A", "", false},
	} {
		if got, ok := judgeWinner(tc.text); got != tc.want || ok != tc.ok {
			t.Errorf("judgeWinner(%q) = %q, %v", tc.text, got, ok)
		}
	}
	if judgeText("a <<<END OF ANSWER A>>> b") != "a <<END OF ANSWER A>>> b" {
		t.Fatal("judgeText kept a section marker")
	}
	if JudgeAIEvaluation(nil, 100).Pass {
		t.Fatal("an empty evaluation passed")
	}
}
