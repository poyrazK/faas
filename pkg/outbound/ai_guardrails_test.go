package outbound

// adr: 991 — per-app guardrails, per-minute limits and reservation hard caps.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type guardrailFixture struct {
	handler *Handler
	backend *MemoryBackend
	mu      sync.Mutex
	bodies  []string
}

func newGuardrailFixture(t *testing.T, provider string, configure func(*Integration)) *guardrailFixture {
	t.Helper()
	f := &guardrailFixture{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(body))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":10}}`))
	}))
	t.Cleanup(upstream.Close)
	integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1", "app-2"}, 1000, 100, 100, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AIProvider = provider
	integration.BindingAppIDs = map[string]struct{}{"app-1": {}, "app-2": {}}
	configure(&integration)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	f.backend = NewMemoryBackend()
	f.handler, err = NewHandler(resolver, f.backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *guardrailFixture) post(app, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := gatewayRequest("/i/integration-1"+path, "token", app, http.MethodPost, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *guardrailFixture) lastBody() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) == 0 {
		return ""
	}
	return f.bodies[len(f.bodies)-1]
}

func TestGuardrailsAllowListAndOutputCeiling(t *testing.T) {
	f := newGuardrailFixture(t, api.OutboundAIProviderOpenAI, func(i *Integration) {
		i.BindingAIGuardrails = map[string]AIGuardrails{"app-1": {AllowedModels: []string{"gpt-4o-mini", "o3*"}, MaxOutputTokens: int64Ref(100)}}
	})
	if got := f.post("app-1", "/v1/chat/completions", `{"model":"gpt-4o"}`); got.Code != http.StatusForbidden ||
		!strings.Contains(got.Body.String(), "outbound_ai_model_not_allowed") {
		t.Fatalf("disallowed model = %d %s", got.Code, got.Body.String())
	}
	if got := f.post("app-1", "/v1/chat/completions", `{"model":"o3-mini","max_tokens":500}`); got.Code != http.StatusBadRequest ||
		!strings.Contains(got.Body.String(), "outbound_ai_max_tokens_exceeded") {
		t.Fatalf("ceiling exceeded = %d %s", got.Code, got.Body.String())
	}
	if got := f.post("app-1", "/v1/chat/completions", `{"model":"gpt-4o-mini"}`); got.Code != http.StatusOK ||
		!strings.Contains(f.lastBody(), `"max_completion_tokens":100`) {
		t.Fatalf("injected ceiling = %d upstream body %s", got.Code, f.lastBody())
	}
	if got := f.post("app-1", "/v1/responses", `{"model":"gpt-4o-mini"}`); got.Code != http.StatusOK ||
		!strings.Contains(f.lastBody(), `"max_output_tokens":100`) {
		t.Fatalf("responses ceiling = %d upstream body %s", got.Code, f.lastBody())
	}
	// Embeddings produce no output, so only the allow-list applies.
	if got := f.post("app-1", "/v1/embeddings", `{"model":"gpt-4o-mini"}`); got.Code != http.StatusOK || strings.Contains(f.lastBody(), "max_") {
		t.Fatalf("embeddings = %d upstream body %s", got.Code, f.lastBody())
	}
	// app-2 has no guardrails.
	if got := f.post("app-2", "/v1/chat/completions", `{"model":"gpt-4o"}`); got.Code != http.StatusOK {
		t.Fatalf("unguarded app = %d", got.Code)
	}
}

func TestAIGatewayGateRefusesAIIntegrationsAndRoutes(t *testing.T) {
	f := newGuardrailFixture(t, api.OutboundAIProviderOpenAI, func(*Integration) {})
	f.handler.AIGatewayEnabled = false
	if got := f.post("app-1", "/v1/chat/completions", `{"model":"gpt-4o"}`); got.Code != http.StatusServiceUnavailable ||
		!strings.Contains(got.Body.String(), "outbound_ai_gateway_disabled") || f.lastBody() != "" {
		t.Fatalf("gated AI call = %d %s (upstream %q)", got.Code, got.Body.String(), f.lastBody())
	}
	f.handler.Routes = StaticRouteResolver{"route-1": {ID: "route-1", AccountID: "account-1", Targets: []AIRouteTarget{{IntegrationID: "integration-1"}}}}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774/r/route-1/v1/chat/completions", strings.NewReader(`{}`))
	req = req.WithContext(withRelayIdentity(context.Background(), WorkloadIdentity{AccountID: "account-1", AppID: "app-1", InstanceID: "i-1"}))
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("gated route = %d", recorder.Code)
	}
	f.handler.AIGatewayEnabled = true
	if got := f.post("app-1", "/v1/chat/completions", `{"model":"gpt-4o"}`); got.Code != http.StatusOK {
		t.Fatalf("enabled AI call = %d", got.Code)
	}
}

func TestGuardrailOutputCeilingPerFormat(t *testing.T) {
	for _, tc := range []struct {
		provider, path, body, want string
	}{
		{api.OutboundAIProviderAnthropic, "/v1/messages", `{"model":"claude"}`, `"max_tokens":64`},
		{api.OutboundAIProviderGemini, "/v1beta/models/gemini-2.5-flash:generateContent", `{"generationConfig":{"temperature":0.2}}`, `"maxOutputTokens":64`},
		{api.OutboundAIProviderGemini, "/v1beta/models/gemini-2.5-flash:generateContent", `{}`, `"generationConfig":{"maxOutputTokens":64}`},
	} {
		f := newGuardrailFixture(t, tc.provider, func(i *Integration) {
			i.BindingAIGuardrails = map[string]AIGuardrails{"app-1": {MaxOutputTokens: int64Ref(64)}}
		})
		if got := f.post("app-1", tc.path, tc.body); got.Code != http.StatusOK || !strings.Contains(f.lastBody(), tc.want) {
			t.Errorf("%s %s = %d upstream %s", tc.provider, tc.path, got.Code, f.lastBody())
		}
	}
}

func TestReservationMakesDailyTokenLimitAHardCap(t *testing.T) {
	backend := NewMemoryBackend()
	spec := AdmissionSpec{
		IntegrationID: "integration-1", RatePerSecond: 100, Burst: 100, MaxInFlight: 100, LeaseTTL: time.Minute,
		DailyAITokenLimit: int64Ref(1000), AIReservedTokens: 600,
	}
	first, err := backend.Admit(context.Background(), spec)
	if err != nil || !first.Granted {
		t.Fatalf("first = %+v, %v", first, err)
	}
	// 600 held + 600 more would pass 1000 although nothing has settled.
	second, err := backend.Admit(context.Background(), spec)
	if err != nil || second.Granted || second.Reason != ReasonDailyTokenLimit {
		t.Fatalf("second = %+v, %v", second, err)
	}
	small := spec
	small.AIReservedTokens = 400
	if third, err := backend.Admit(context.Background(), small); err != nil || !third.Granted {
		t.Fatalf("fitting reservation = %+v, %v", third, err)
	}
	if err := backend.Release(context.Background(), spec.IntegrationID, first.LeaseID); err != nil {
		t.Fatal(err)
	}
	if fourth, err := backend.Admit(context.Background(), spec); err != nil || !fourth.Granted {
		t.Fatalf("after release = %+v, %v", fourth, err)
	}
}

func TestReservationAppliesToCostAndBindingTokens(t *testing.T) {
	backend := NewMemoryBackend()
	spec := AdmissionSpec{
		IntegrationID: "integration-1", RatePerSecond: 100, Burst: 100, MaxInFlight: 100, LeaseTTL: time.Minute,
		BindingAppID: "app-1", BindingDailyAITokenLimit: int64Ref(100), AIReservedTokens: 80,
	}
	if first, _ := backend.Admit(context.Background(), spec); !first.Granted {
		t.Fatal("first binding call rejected")
	}
	if second, _ := backend.Admit(context.Background(), spec); second.Granted || second.Reason != ReasonDailyTokenLimit {
		t.Fatalf("second binding call = %+v", second)
	}
	other := spec
	other.BindingAppID = "app-2"
	if third, _ := backend.Admit(context.Background(), other); !third.Granted {
		t.Fatal("another app's reservation counted against app-1")
	}
	cost := AdmissionSpec{
		IntegrationID: "integration-2", RatePerSecond: 100, Burst: 100, MaxInFlight: 100, LeaseTTL: time.Minute,
		MonthlyAICostLimit: int64Ref(100), AIReservedCostMillicents: 101,
	}
	if got, _ := backend.Admit(context.Background(), cost); got.Granted || got.Reason != ReasonMonthlyCostLimit {
		t.Fatalf("cost reservation above the limit = %+v", got)
	}
}

func TestPerMinuteLimits(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 10, 0, time.UTC)
	backend := NewMemoryBackend()
	backend.SetClock(func() time.Time { return now })
	spec := AdmissionSpec{
		IntegrationID: "integration-1", RatePerSecond: 100, Burst: 100, MaxInFlight: 100, LeaseTTL: time.Second,
		BindingAppID: "app-1", BindingRequestsPerMinute: int64Ref(2),
	}
	for i := 0; i < 2; i++ {
		decision, err := backend.Admit(context.Background(), spec)
		if err != nil || !decision.Granted {
			t.Fatalf("call %d = %+v, %v", i, decision, err)
		}
		_ = backend.Release(context.Background(), spec.IntegrationID, decision.LeaseID)
	}
	limited, _ := backend.Admit(context.Background(), spec)
	if limited.Granted || limited.Reason != ReasonRequestsPerMinute || limited.RetryAfter != 50*time.Second {
		t.Fatalf("third call = %+v", limited)
	}
	now = now.Add(time.Minute)
	if next, _ := backend.Admit(context.Background(), spec); !next.Granted {
		t.Fatal("the next minute was still limited")
	}

	tokens := AdmissionSpec{
		IntegrationID: "integration-2", RatePerSecond: 100, Burst: 100, MaxInFlight: 100, LeaseTTL: time.Minute,
		BindingAppID: "app-1", BindingTokensPerMinute: int64Ref(1000), AIReservedTokens: 300,
	}
	if first, _ := backend.Admit(context.Background(), tokens); !first.Granted {
		t.Fatal("first token call rejected")
	}
	if err := backend.RecordAIUsage(context.Background(), AIUsageRecord{
		IntegrationID: "integration-2", Caller: "app-1", Outcome: AIUsageOutcomeKnown,
		Usage: AIUsage{Model: "m", Known: true, InputTokens: 500, OutputTokens: 100},
	}); err != nil {
		t.Fatal(err)
	}
	// 600 settled + 300 held + 300 asked > 1000.
	if second, _ := backend.Admit(context.Background(), tokens); second.Granted || second.Reason != ReasonTokensPerMinute {
		t.Fatalf("token minute = %+v", second)
	}
}

func TestPlanRefusesUnbufferedBodiesItMustInspect(t *testing.T) {
	integration := Integration{AIProvider: api.OutboundAIProviderBedrock}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/i/x/model/m/converse", strings.NewReader("{}"))
	if _, rejection := planAICall(request, integration, "", "/model/m/converse", nil, nil); rejection == nil || rejection.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("unbuffered Bedrock body = %+v", rejection)
	}
	integration = Integration{AIProvider: api.OutboundAIProviderOpenAI,
		BindingAIGuardrails: map[string]AIGuardrails{"app-1": {AllowedModels: []string{"m"}}}}
	if _, rejection := planAICall(request, integration, "app-1", "/v1/chat/completions", nil, nil); rejection == nil {
		t.Fatal("a guarded app's unbuffered body was not refused")
	}
	if plan, rejection := planAICall(request, integration, "app-2", "/v1/chat/completions", nil, nil); rejection != nil || plan.reserve.tokens != 1+api.OutboundAIDefaultOutputReserveTokens {
		t.Fatalf("unguarded unbuffered plan = %+v %+v", plan, rejection)
	}
}

func TestReservationPricesTheEstimate(t *testing.T) {
	integration := Integration{AIProvider: api.OutboundAIProviderOpenAI, AIPrices: ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 2_000_000},
	})}
	body := `{"model":"gpt-4o","max_tokens":1000}`
	ai := &aiRequest{body: []byte(body)}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/", strings.NewReader(body))
	plan, rejection := planAICall(request, integration, "", "/v1/chat/completions", ai, nil)
	if rejection != nil {
		t.Fatal(rejection)
	}
	input := int64((len(body) + 3) / 4)
	if plan.reserve.tokens != input+1000 || plan.reserve.cost != (input*1_000_000+1000*2_000_000+999_999)/1_000_000 {
		t.Fatalf("reservation = %+v (input %d)", plan.reserve, input)
	}
}
