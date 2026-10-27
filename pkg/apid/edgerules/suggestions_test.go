package edgerules

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// suggestionHost is the slice of apid the suggestions handler uses.
type suggestionHost struct {
	store state.Store
	app   state.App
}

func (h suggestionHost) LoadApp(w http.ResponseWriter, _ *http.Request, _ state.Account, slug string) (state.App, bool) {
	if slug != h.app.Slug {
		w.WriteHeader(http.StatusNotFound)
		return state.App{}, false
	}
	return h.app, true
}
func (h suggestionHost) Store() state.Store                                         { return h.store }
func (h suggestionHost) Logger() *slog.Logger                                       { return slog.New(slog.DiscardHandler) }
func (h suggestionHost) EmitAudit(context.Context, string, *string, map[string]any) {}
func (h suggestionHost) Notifier() Notifier                                         { return nil }
func (h suggestionHost) PromQL() *promql.Client                                     { return nil }
func (h suggestionHost) PlatformFlagEnabled(string, state.Account) bool             { return true }

// telemetryStore serves canned request telemetry and records the window read.
type telemetryStore struct {
	MemStore *state.MemStore
	families []sqlc.RequestTelemetryAnalyticsByDimensionRow
	routes   []sqlc.RequestTelemetryAnalyticsByRouteRow
	err      error
	dim      sqlc.RequestTelemetryAnalyticsByDimensionParams
	calls    int
}

func (s *telemetryStore) RequestTelemetryAnalyticsByDimension(_ context.Context, p sqlc.RequestTelemetryAnalyticsByDimensionParams) ([]sqlc.RequestTelemetryAnalyticsByDimensionRow, error) {
	s.dim = p
	s.calls++
	return s.families, s.err
}

func (s *telemetryStore) RequestTelemetryAnalyticsByRoute(_ context.Context, _ sqlc.RequestTelemetryAnalyticsByRouteParams) ([]sqlc.RequestTelemetryAnalyticsByRouteRow, error) {
	s.calls++
	return s.routes, s.err
}

// adr: 20261020105000 — the suggestions route reads the plan's telemetry window
// (gated on plans without request telemetry), turns bot share and route
// counts into recipe suggestions, and skips paths existing rules cover.
func TestGetEdgeRuleSuggestions(t *testing.T) {
	mem := state.NewMemStore()
	app := state.App{ID: "0b8f6a52-3f4e-4f39-9a51-5d1c0f6f8a11", AccountID: "5a0c2b8e-1d7f-4c3a-8e6b-2f9d4a7c1e33", Slug: "shop"}
	fake := &telemetryStore{
		MemStore: mem,
		families: []sqlc.RequestTelemetryAnalyticsByDimensionRow{
			{Dimension: "browser", Requests: 6000}, {Dimension: []byte("bot"), Requests: 1500}, {Dimension: "other", Requests: 500},
		},
		routes: []sqlc.RequestTelemetryAnalyticsByRouteRow{
			{Route: "POST /login", Method: "POST", Requests: 900, ErrorRequests: 400},
			{Route: "POST /api/session", Method: "POST", Requests: 700},
			{Route: "GET /admin/users", Method: "GET", Requests: 40},
			{Route: "__route_other__", Method: "GET", Requests: 5000},
		},
	}
	svc := &Service{Host: suggestionHost{store: statetest.NewMem(fake), app: app}}
	get := func(plan api.Plan) (*httptest.ResponseRecorder, api.EdgeRuleRecipeSuggestionsResponse) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/apps/shop/edge-rules/suggestions", nil)
		req.SetPathValue("slug", "shop")
		rec := httptest.NewRecorder()
		svc.GetEdgeRuleSuggestions(rec, req, state.Account{ID: app.AccountID, Plan: plan})
		var out api.EdgeRuleRecipeSuggestionsResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
		return rec, out
	}

	if rec, _ := get(api.PlanFree); rec.Code != http.StatusPaymentRequired || fake.calls != 0 {
		t.Fatalf("free: status %d after %d telemetry reads, want 402 before any", rec.Code, fake.calls)
	}

	rec, out := get(api.PlanPro)
	if rec.Code != http.StatusOK || out.Window != "7d" || time.Since(out.Since) < 7*24*time.Hour-time.Minute {
		t.Fatalf("pro: status %d window %q since %v", rec.Code, out.Window, out.Since)
	}
	if !fake.dim.AppID.Valid || !fake.dim.AccountID.Valid || fake.dim.GroupBy != "ua_family" {
		t.Fatalf("dimension query = %+v", fake.dim)
	}
	got := map[string]string{}
	for _, sg := range out.Suggestions {
		got[sg.Recipe+":"+sg.Params["path"]] = sg.Reason
	}
	for _, want := range []string{"block-unverified-bots:", "rate-limit-path:/login", "rate-limit-path:/api/session", "allow-ips-only:/admin/**"} {
		if got[want] == "" {
			t.Errorf("pro: missing suggestion %s in %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("pro: suggestions = %v, want exactly 4", got)
	}

	if _, err := mem.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: app.AccountID, AppID: app.ID, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/api/**", Enabled: true, Kind: state.EdgeRuleKindThrottle,
	}); err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	rec, out = get(api.PlanHobby)
	if rec.Code != http.StatusOK || out.Window != "3d" {
		t.Fatalf("hobby: status %d window %q", rec.Code, out.Window)
	}
	for _, sg := range out.Suggestions {
		if sg.Params["path"] == "/api/session" {
			t.Fatalf("hobby: suggested a path the throttle rule covers: %+v", sg)
		}
	}

	fake.err = errors.New("telemetry down")
	if rec, _ := get(api.PlanPro); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("store error: status %d, want 503", rec.Code)
	}
}
