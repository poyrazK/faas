package aiconsole

// adr: 20261022145950 — the AI gateway's dashboard pages list the
// account's AI integrations, show one integration's day by model, caller,
// tag and end user with latency, alerts and traces, explore traces, and
// draw one trace's calls on a timeline.

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// testHost renders the real templates for a memory store.
type testHost struct {
	store *state.MemStore
	gate  string
}

func (h testHost) Store() state.Store        { return h.store }
func (h testHost) Gate(state.Account) string { return h.gate }
func (h testHost) Render(w http.ResponseWriter, _ *http.Request, acct state.Account, page dashboard.Page) {
	page.Account = &dashboard.AccountView{ID: acct.ID, Email: acct.Email, Plan: string(acct.Plan), EmailVerified: true}
	if err := dashboard.Render(w, slog.Default(), "", page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type fixture struct {
	service *Service
	host    *testHost
	store   *state.MemStore
	acct    state.Account
	app     state.App
	ai      string
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "ai-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "chat-api", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) string {
		offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
			ID: uuid.NewString(), AccountID: acct.ID, Name: name, Origin: "https://api.openai.com",
			AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
			CredentialSource: "customer_sealed", OwnerKind: "customer",
			RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000},
		})
		if err != nil {
			t.Fatal(err)
		}
		return offer.ID
	}
	ai := create("llm")
	create("payments") // not an AI integration
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, ai, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI,
		RequestLogEnabled: true, QualityAlerts: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	host := &testHost{store: store}
	return &fixture{service: &Service{Host: host, Now: func() time.Time { return now }}, host: host, store: store, acct: acct,
		app: app, ai: ai, now: now}
}

func (f *fixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	f.service.Serve(w, httptest.NewRequest(http.MethodGet, target, nil), f.acct)
	return w
}

func (f *fixture) seed(t *testing.T) {
	t.Helper()
	day := f.now.Format(dayLayout)
	f.store.SeedOutboundAIUsage(f.ai, day,
		state.OutboundAIUsageItem{Caller: f.app.ID, Model: "gpt-5", Requests: 90, ErrorRequests: 9, InputTokens: 9_000, OutputTokens: 3_000,
			CostMillicents: 300_000, TruncatedRequests: 4, RefusedRequests: 1, GoodFeedback: 7, BadFeedback: 2, EvaluatedRequests: 10,
			EvaluationScoreSum: 42},
		state.OutboundAIUsageItem{Caller: "runs", Model: "gpt-5-mini", Requests: 10, InputTokens: 500, OutputTokens: 100,
			CostMillicents: 100_000})
	f.store.SeedOutboundAITagUsage(f.ai, day, state.OutboundAITagUsageItem{Tag: "feature=search", Requests: 60, CostMillicents: 250_000})
	f.store.SeedOutboundAIUserUsage(f.ai, day, state.OutboundAIUserUsageItem{EndUser: "u_42", Requests: 5, CostMillicents: 40_000})
	f.store.SeedOutboundAIQualityAlert(f.ai, state.OutboundAIQualityAlert{ID: uuid.NewString(), IntegrationID: f.ai, UsageDate: day,
		Model: "gpt-5", Metric: "truncated", CurrentValue: 1_250, BaselineValue: 200, CurrentCount: 400, BaselineCount: 9_000,
		DetectedAt: f.now})
	ttfb := int64(300)
	score := int32(2)
	f.store.SeedOutboundAITrace(f.ai, state.OutboundAITrace{TraceID: "task/1", Caller: f.app.ID, EndUser: "u_42",
		FirstAt: f.now.Add(-3 * time.Second), LastAt: f.now, Calls: 2, ErrorCalls: 1, CostMillicents: 6_000, ToolCalls: 1,
		RepeatedToolCalls: 1, Models: []string{"gpt-5"}})
	f.store.SeedOutboundAIRequestLog(f.ai,
		state.OutboundAIRequestLogEntry{ID: uuid.NewString(), CreatedAt: f.now.Add(-2 * time.Second), Caller: f.app.ID, Model: "gpt-5",
			Status: 200, Outcome: "known", InputTokens: 100, OutputTokens: 50, CostMillicents: 3_000, DurationMS: 1_000, TTFBMS: &ttfb,
			TraceID: "task/1", ToolCalls: []string{"lookup"}, QualityFlags: []string{"truncated"}, Feedback: "bad",
			FeedbackLabel: "wrong_answer", EvaluationScore: &score},
		state.OutboundAIRequestLogEntry{ID: uuid.NewString(), CreatedAt: f.now, Caller: f.app.ID, Model: "gpt-5", Status: 502,
			Outcome: "error", DurationMS: 500, TraceID: "task/1"})
}

func TestOverviewListsAIIntegrations(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	w := f.get(t, "/dashboard/ai")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `href="/dashboard/ai/`+f.ai+`"`) || strings.Contains(body, "payments") ||
		!strings.Contains(body, "USD 4.00000") || !strings.Contains(body, "9.0%") || !strings.Contains(body, "4.20") {
		t.Fatalf("overview = %d\n%s", w.Code, body)
	}
	f.host.gate = "Your plan does not include the AI gateway."
	if w := f.get(t, "/dashboard/ai/"+f.ai); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "does not include the AI gateway") ||
		strings.Contains(w.Body.String(), "By model") {
		t.Fatalf("gated page = %d\n%s", w.Code, w.Body.String())
	}
}

func TestIntegrationPageShowsTheDay(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	w := f.get(t, "/dashboard/ai/"+f.ai)
	body := w.Body.String()
	for _, want := range []string{
		"gpt-5-mini", "chat-api", "Runs", "truncated 4, refused 1", "7 good / 2 bad", "4.20", `width="75"`,
		"feature=search", "u_42", "First byte p50", "300 ms", "50.0%", "Over the latest 2 logged calls",
		"12.50%", "2.00%", `href="/dashboard/ai/` + f.ai + `/traces/task%2F1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("integration page lacks %q", want)
		}
	}
	if w.Code != http.StatusOK || t.Failed() {
		t.Fatalf("integration page = %d\n%s", w.Code, body)
	}
	if w := f.get(t, "/dashboard/ai/"+f.ai+"?date=2001-01-01"); !strings.Contains(w.Body.String(), "within the last 90 days") {
		t.Fatalf("an old day = %s", w.Body.String())
	}
	if w := f.get(t, "/dashboard/ai/"+uuid.NewString()); w.Code != http.StatusNotFound {
		t.Fatalf("an unknown integration = %d", w.Code)
	}
	ctx := context.Background()
	if err := f.store.SetOutboundAIPolicy(ctx, f.acct.ID, f.ai, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	if w := f.get(t, "/dashboard/ai/"+f.ai); !strings.Contains(w.Body.String(), "--request-log on") {
		t.Fatalf("latency without the request log = %s", w.Body.String())
	}
}

func TestTraceExplorerAndTimeline(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	w := f.get(t, "/dashboard/ai/"+f.ai+"/traces?app_id="+f.app.ID)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "task/1") || !strings.Contains(w.Body.String(), `selected>chat-api`) {
		t.Fatalf("traces = %d\n%s", w.Code, w.Body.String())
	}
	if w := f.get(t, "/dashboard/ai/"+f.ai+"/traces?app_id="+uuid.NewString()); !strings.Contains(w.Body.String(), "No traces match") {
		t.Fatalf("another app's traces = %s", w.Body.String())
	}
	if w := f.get(t, "/dashboard/ai/"+f.ai+"/traces?before=yesterday"); !strings.Contains(w.Body.String(), "filter is not valid") {
		t.Fatalf("a bad cursor = %s", w.Body.String())
	}
	w = f.get(t, "/dashboard/ai/"+f.ai+"/traces/"+url.PathEscape("task/1"))
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Count(body, `<rect class="call`) != 2 || !strings.Contains(body, `class="call error"`) ||
		!strings.Contains(body, `class="call flagged"`) || !strings.Contains(body, "lookup") || !strings.Contains(body, "bad (wrong_answer)") ||
		!strings.Contains(body, `class="ttfb"`) || !strings.Contains(body, "wrong_answer") {
		t.Fatalf("trace = %d\n%s", w.Code, body)
	}
	if w := f.get(t, "/dashboard/ai/"+f.ai+"/traces/missing"); w.Code != http.StatusNotFound {
		t.Fatalf("a missing trace = %d", w.Code)
	}
	other, err := f.store.CreateAccount(context.Background(), "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.acct = other
	if w := f.get(t, "/dashboard/ai/"+f.ai+"/traces/"+url.PathEscape("task/1")); w.Code != http.StatusNotFound {
		t.Fatalf("another account's trace = %d", w.Code)
	}
}

func TestTraceExplorerPagesOlderTraces(t *testing.T) {
	f := newFixture(t)
	for index := range tracesPageSize + 1 {
		at := f.now.Add(-time.Duration(index) * time.Minute)
		f.store.SeedOutboundAITrace(f.ai, state.OutboundAITrace{TraceID: "t" + strconv.Itoa(index), Caller: f.app.ID, FirstAt: at, LastAt: at, Calls: 1})
	}
	w := f.get(t, "/dashboard/ai/"+f.ai+"/traces")
	body := w.Body.String()
	if strings.Count(body, `"><code>t`) != tracesPageSize || !strings.Contains(body, "Older traces") || !strings.Contains(body, "before=") {
		t.Fatalf("first page:\n%s", body)
	}
}

func TestTimelinePlacesCallsOnOneAxis(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ttfb := int64(250)
	view := timeline([]state.OutboundAIRequestLogEntry{
		{CreatedAt: start.Add(time.Second), DurationMS: 1_000, TTFBMS: &ttfb, Model: "a", Status: 200},
		{CreatedAt: start.Add(4 * time.Second), DurationMS: 2_000, Model: "b", Status: 500},
	})
	if len(view.Bars) != 2 || view.Bars[0].X != 0 || view.Bars[0].W != 250 || view.Bars[0].TTFBX != 62 ||
		view.Bars[1].X != 500 || view.Bars[1].W != 500 || !view.Bars[1].Error || view.Bars[1].HasTTFB {
		t.Fatalf("bars = %+v", view.Bars)
	}
	if len(view.Ticks) != timelineTicks+1 || view.Ticks[timelineTicks].Label != "4.0 s" {
		t.Fatalf("ticks = %+v", view.Ticks)
	}
}

func TestFormatting(t *testing.T) {
	for got, want := range map[string]string{
		money(123_456_789): "USD 1234.56789", count(1_234_567): "1,234,567", count(999): "999", percent(1, 3): "33.3%",
		percent(1, 0): "—", duration(999): "999 ms", duration(1_500): "1.5 s", hundredths(420): "4.20",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
