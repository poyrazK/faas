// adr: 531
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

type previewParityMatcher struct {
	noOpEdgeRuleMatcher
	entry HostEntry
}

func (m previewParityMatcher) MatchBudget(ctx context.Context, _ string, path, method string) *EdgeRuleBudgetResolved {
	return PickFirstBudgetMatch(m.entry.Budget, path, method, EdgeRuleRequestHeaders(ctx))
}

func (m previewParityMatcher) MatchRewrite(ctx context.Context, _ string, path, method string) *EdgeRuleRewriteResolved {
	return PickFirstRewriteMatch(m.entry.Rewrite, path, method, EdgeRuleRequestHeaders(ctx))
}

func (m previewParityMatcher) MatchHeaders(ctx context.Context, _ string, path, method string) *EdgeRuleHeadersResolved {
	return PickFirstHeadersMatch(m.entry.Headers, path, method, EdgeRuleRequestHeaders(ctx))
}

func previewParityRule(t *testing.T, host, id, kind, path string, priority int, action any) api.EdgeRuleResponse {
	t.Helper()
	raw, err := json.Marshal(map[string]any{kind: action})
	if err != nil {
		t.Fatal(err)
	}
	return api.EdgeRuleResponse{ID: id, Kind: kind, MatchHost: host, MatchPath: path, Priority: priority, Enabled: true, Action: raw}
}

func TestTrafficPreviewAgreesWithForwardedExecutionBudget(t *testing.T) {
	cases := []struct {
		name         string
		ingressTotal int
		headerAction string
		selector     bool
		override     string
		wantRule     string
		wantMS       int64
		wantTotal    int64
		wantSource   string
	}{
		{name: "total_pins_original_rule", ingressTotal: 4000, wantRule: "original", wantMS: 1200, wantTotal: 4000, wantSource: "rule"},
		{name: "execution_only_uses_rewritten_rule", wantRule: "rewritten", wantMS: 2400, wantSource: "rule"},
		{name: "large_override_cannot_wrap_or_extend_total", ingressTotal: 4000, override: "18446744078709", wantRule: "original", wantMS: api.RequestBudgetMax.Milliseconds(), wantTotal: 4000, wantSource: "ceiling_clamp"},
		{name: "header_action_cannot_create_selector", headerAction: "set", selector: true, wantMS: api.RequestBudgetDefault.Milliseconds(), wantSource: "plan_default"},
		{name: "header_action_cannot_remove_selector", headerAction: "remove", selector: true, wantRule: "rewritten", wantMS: 3000, wantSource: "header_override"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			backend.setLegacyHot()
			var logs bytes.Buffer
			h.log = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			signer, err := trafficdeadline.New(bytes.Repeat([]byte{42}, 32), nil)
			if err != nil {
				t.Fatal(err)
			}
			h.WithTrafficDeadlines(signer)
			original := EdgeRuleBudgetResolved{ID: "original", AccountID: backend.app.AccountID, AppID: backend.app.ID, Priority: 20, PathGlob: "/public", BudgetMs: 1200, TotalDeadlineMs: tc.ingressTotal}
			rewritten := EdgeRuleBudgetResolved{ID: "rewritten", AccountID: backend.app.AccountID, AppID: backend.app.ID, Priority: 10, PathGlob: "/backend", BudgetMs: 2400, TotalDeadlineMs: 2000}
			if tc.selector {
				original.MatchHeaders = map[string]string{"X-Selector": "original"}
				rewritten.MatchHeaders = map[string]string{"X-Selector": "original"}
			}
			matcher := previewParityMatcher{entry: HostEntry{
				Budget:  []EdgeRuleBudgetResolved{rewritten, original},
				Rewrite: []EdgeRuleRewriteResolved{{ID: "rewrite", AccountID: backend.app.AccountID, From: "/public", To: "/backend"}},
			}}
			rules := []api.EdgeRuleResponse{
				previewParityRule(t, backend.host, "original", "budget", "/public", 20, api.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: tc.ingressTotal}),
				previewParityRule(t, backend.host, "rewritten", "budget", "/backend", 10, api.EdgeRuleBudgetAction{BudgetMs: 2400, TotalDeadlineMs: 2000}),
				previewParityRule(t, backend.host, "rewrite", "rewrite", "*", 1, api.EdgeRuleRewriteAction{From: "/public", To: "/backend"}),
			}
			rules[0].MatchHeaders, rules[1].MatchHeaders = original.MatchHeaders, rewritten.MatchHeaders
			req := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/public", nil)
			if tc.override != "" {
				req.Header.Set(api.RequestBudgetDefaultOverrideHeader, tc.override)
			}
			if tc.headerAction != "" {
				ops := []api.EdgeRuleHeaderOp{{Name: "X-Selector", Action: tc.headerAction, Value: "original"}, {Name: api.RequestBudgetDefaultOverrideHeader, Action: "set", Value: "3000"}}
				matcher.entry.Headers = []EdgeRuleHeadersResolved{{AccountID: backend.app.AccountID, RequestHeaders: []EdgeRuleHeaderOp{{Name: "X-Selector", Action: tc.headerAction, Value: "original"}, {Name: api.RequestBudgetDefaultOverrideHeader, Action: "set", Value: "3000"}}}}
				rules = append(rules, previewParityRule(t, backend.host, "headers", "headers", "*", 1, api.EdgeRuleHeadersAction{RequestHeaders: ops}))
				if tc.headerAction == "remove" {
					req.Header.Add("X-Selector", "original")
				}
			}
			limits, _ := api.LimitsFor(backend.app.Plan)
			input := edgeruletrace.Input{App: "demo", Host: backend.host, Path: "/public", Method: http.MethodGet, Headers: req.Header.Clone(),
				AppMaintenanceLoaded: true, AppRequestBudgetLoaded: true,
				RequestBudgetMS: limits.RequestBudgetForType(string(backend.app.Type)).Milliseconds(), RequestBudgetMaxMS: limits.RequestBudgetMaxDuration().Milliseconds()}
			preview, err := edgeruletrace.Simulate(input, rules)
			if err != nil {
				t.Fatal(err)
			}
			h.WithEdgeRules(matcher, nil, nil)
			forwarded := false
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwarded = true
					if r.URL.Path != "/backend" {
						t.Errorf("forwarded path = %s", r.URL.Path)
					}
					if tc.wantTotal > 0 {
						started, _ := StartTimeFromContext(r.Context())
						deadline, ok := r.Context().Deadline()
						if !ok || deadline.After(started.Add(time.Duration(tc.wantTotal)*time.Millisecond)) {
							t.Error("forwarded execution extended the pinned ingress deadline")
						}
					}
					w.WriteHeader(http.StatusNoContent)
				})
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if !forwarded || rec.Code != http.StatusNoContent {
				t.Fatalf("forwarded=%t status=%d body=%s", forwarded, rec.Code, rec.Body.String())
			}
			last := preview.Simulation.Steps[len(preview.Simulation.Steps)-1]
			if last.RuleID != tc.wantRule || last.BudgetPolicy == nil || last.BudgetPolicy.BudgetMS != tc.wantMS || last.BudgetPolicy.Source != tc.wantSource || last.BudgetPolicy.TotalDeadlineMS != tc.wantTotal {
				t.Fatalf("preview execution = %+v", last)
			}
			if (preview.Simulation.TotalDeadlinePolicy != nil) != (tc.wantTotal > 0) {
				t.Fatalf("preview ingress policy = %+v", preview.Simulation.TotalDeadlinePolicy)
			}
			stamps := 0
			for _, line := range bytes.Split(logs.Bytes(), []byte{'\n'}) {
				var row map[string]any
				if json.Unmarshal(line, &row) == nil && row["msg"] == "budget_stamped" {
					stamps++
					if row["budget_ms"] != float64(tc.wantMS) || row["source"] != tc.wantSource {
						t.Fatalf("runtime stamp differs from preview: %+v", row)
					}
				}
			}
			if stamps != 1 {
				t.Fatalf("execution stamp count = %d", stamps)
			}
		})
	}
}

func TestTrafficPreviewAgreesWithRuntimeRetryMethodGuard(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPost, http.MethodPatch, "CUSTOM"} {
		for _, allow := range []bool{false, true} {
			for _, key := range []string{"", "   ", "secret-key-value"} {
				t.Run(method+"/"+map[bool]string{false: "disabled", true: "opted_in"}[allow]+"/"+key, func(t *testing.T) {
					req := httptest.NewRequest(method, "http://example.com/orders", nil)
					req.Header.Set("Idempotency-Key", key)
					rule := previewParityRule(t, "example.com", "retry", "retry", "*", 1, api.EdgeRuleRetryAction{MaxAttempts: 2, AllowNonIdempotent: allow})
					preview, err := edgeruletrace.Simulate(edgeruletrace.Input{App: "demo", Host: "example.com", Path: "/orders", Method: method, Headers: req.Header, AppMaintenanceLoaded: true}, []api.EdgeRuleResponse{rule})
					if err != nil {
						t.Fatal(err)
					}
					eligibility := preview.Simulation.Steps[0].RetryPolicy.MethodEligibility
					allowed, reason := (RetryPolicy{AllowNonIdempotent: allow}).retryable(req)
					previewAllowed := eligibility == "idempotent_method" || eligibility == "non_idempotent_allowed_with_key"
					if allowed != previewAllowed || (eligibility == "idempotency_key_required" && reason != RetrySkipIdempotency) {
						t.Fatalf("preview=%s runtime allowed=%t reason=%s", eligibility, allowed, reason)
					}
				})
			}
		}
	}
}
