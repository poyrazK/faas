package main

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 974 — edge-rule activity alerts accept a rule of the same app,
// hourly windows and webhook only; the rule id round-trips and the metric
// family cannot change on update.
func TestEdgeRuleActivityAlerts(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "edge-alerts")
	other := mustSeedEdgeRuleApp(t, e, "edge-alerts-other")
	rule := mustCreateEdgeRule(t, e, slug, edgeRuleRouteReq(slug))
	foreign := mustCreateEdgeRule(t, e, other, edgeRuleRouteReq(other))

	req := alertRuleReq()
	req.Name, req.Metric, req.WindowSpec, req.Threshold, req.EdgeRuleID = "block spike", "edge_rule_hits", "1h", 1000, rule.ID
	created := mustCreateAlertRule(t, e, slug, req)
	if created.EdgeRuleID != rule.ID || created.Metric != "edge_rule_hits" {
		t.Fatalf("created = %+v, want edge_rule_id %s", created, rule.ID)
	}

	appWide := alertRuleReq()
	appWide.Name, appWide.Metric, appWide.WindowSpec = "log-mode would act", "edge_rule_log_hits", "24h"
	if got := mustCreateAlertRule(t, e, slug, appWide); got.EdgeRuleID != "" {
		t.Fatalf("app-wide alert carries edge_rule_id %q", got.EdgeRuleID)
	}

	rollback := "rollback"
	for name, tc := range map[string]struct {
		mutate func(*api.CreateAlertRuleRequest)
		status int
	}{
		"sub-hour window":    {func(r *api.CreateAlertRuleRequest) { r.WindowSpec = "15m" }, http.StatusBadRequest},
		"beyond retention":   {func(r *api.CreateAlertRuleRequest) { r.WindowSpec = "15d" }, http.StatusBadRequest},
		"rollback action":    {func(r *api.CreateAlertRuleRequest) { r.Action = &rollback }, http.StatusBadRequest},
		"malformed rule id":  {func(r *api.CreateAlertRuleRequest) { r.EdgeRuleID = "not-a-uuid" }, http.StatusBadRequest},
		"other app's rule":   {func(r *api.CreateAlertRuleRequest) { r.EdgeRuleID = foreign.ID }, http.StatusNotFound},
		"rule id on latency": {func(r *api.CreateAlertRuleRequest) { r.Metric, r.WindowSpec = "latency_p99_ms", "5m" }, http.StatusBadRequest},
		"unknown rule id":    {func(r *api.CreateAlertRuleRequest) { r.EdgeRuleID = "33333333-3333-4333-8333-333333333333" }, http.StatusNotFound},
	} {
		bad := req
		bad.Name = "bad " + name
		tc.mutate(&bad)
		if rec := e.do(t, "POST", "/v1/apps/"+slug+"/alerts", bad, nil); rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", name, rec.Code, tc.status, rec.Body.String())
		}
	}

	logHits := "edge_rule_log_hits"
	if rec := e.do(t, "PATCH", "/v1/apps/"+slug+"/alerts/"+created.ID, api.UpdateAlertRuleRequest{Metric: &logHits}, nil); rec.Code != http.StatusOK {
		t.Fatalf("switch within family: %d %s", rec.Code, rec.Body.String())
	}
	latency := "latency_p99_ms"
	rec := e.do(t, "PATCH", "/v1/apps/"+slug+"/alerts/"+created.ID, api.UpdateAlertRuleRequest{Metric: &latency}, nil)
	assertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
	short := "5m"
	rec = e.do(t, "PATCH", "/v1/apps/"+slug+"/alerts/"+created.ID, api.UpdateAlertRuleRequest{WindowSpec: &short}, nil)
	assertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
}
