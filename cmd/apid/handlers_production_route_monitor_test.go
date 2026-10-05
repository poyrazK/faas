package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

// ADR-498: independent advisory intent, MFA/read-write scopes and strict budget input.
func TestProductionRouteMonitorAPIConfigurationScopeAndWorker(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "production-routes-api")
	base := "/v1/apps/" + slug + "/route-monitor"
	rec := e.do(t, "GET", base, nil, nil)
	var c api.RouteMonitorConfig
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &c) != nil || routemonitor.ValidateConfig(c) != nil || c.Enabled {
		t.Fatalf("default %d %s", rec.Code, rec.Body)
	}
	budget := int64(100)
	req := api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: &c.Revision, Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget, MaxP95MS: 300}}}
	rec = e.do(t, "PUT", base, req, nil)
	if rec.Code != 200 {
		t.Fatalf("set %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PUT", base, req, nil); rec.Code != 409 {
		t.Fatal("stale update accepted")
	}
	rec = e.do(t, "GET", base+"/report", nil, nil)
	var report api.RouteMonitorReport
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil || report.Status != "unknown" || routemonitor.ValidateReport(report) != nil {
		t.Fatalf("report %d %s", rec.Code, rec.Body)
	}
	if n, err := e.s.drainRouteMonitors(t.Context()); err != nil || n != 1 {
		t.Fatalf("worker %d %v", n, err)
	}
	if rec := e.do(t, "GET", base+"/incidents", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"incidents":[]`) {
		t.Fatal("unknown created incident")
	}
	for _, query := range []string{"?limit=0", "?limit=11", "?limit=1&limit=2", "?before=bad", "?route=checkout"} {
		if rec := e.do(t, "GET", base+"/incidents"+query, nil, nil); rec.Code != 400 {
			t.Fatalf("bad query %s accepted: %d", query, rec.Code)
		}
	}
	if rec := e.do(t, "GET", base+"/incidents/"+uuid.NewString(), nil, nil); rec.Code != 404 {
		t.Fatal("missing incident leaked")
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "monitor-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "GET", base, nil, nil); rec.Code != 200 {
		t.Fatal("read scope rejected")
	}
	if rec := e.do(t, "PUT", base, req, nil); rec.Code != 403 {
		t.Fatal("read scope wrote intent")
	}
	if rec := e.do(t, "GET", "/v1/apps/missing-production-app/route-monitor", nil, nil); rec.Code != 404 {
		t.Fatal("app scope leak")
	}
}
func TestProductionRouteMonitorAPIMFAPlanAndStrictInput(t *testing.T) {
	e := setup(t, api.PlanFree)
	slug := mustSeedEdgeRuleApp(t, e, "production-free")
	base := "/v1/apps/" + slug + "/route-monitor"
	req := api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: new(int64), Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/", MaxP95MS: 100}}}
	if rec := e.do(t, "PUT", base, req, nil); rec.Code != 402 {
		t.Fatalf("free enabled: %d %s", rec.Code, rec.Body)
	}
	req.Enabled = false
	if rec := e.do(t, "PUT", base, req, nil); rec.Code != 200 {
		t.Fatal("downgrade disable unavailable")
	}
	if rec := e.do(t, "GET", base+"/incidents", nil, nil); rec.Code != 402 {
		t.Fatal("entitlement bypassed")
	}
	pro := setup(t, api.PlanPro)
	app := mustSeedEdgeRuleApp(t, pro, "production-strict")
	path := "/v1/apps/" + app + "/route-monitor"
	for _, body := range []string{`{"enabled":true,"expected_revision":0,"routes":[{"method":"POST","path":"/checkout"}]}`, `{"enabled":true,"expected_revision":0,"routes":[{"method":"POST","path":"/checkout","max_p95_ms":100,"typo":1}]}`, `{"enabled":true,"routes":[{"method":"POST","path":"/checkout","max_p95_ms":100}]}`, `{"enabled":true,"expected_revision":0,"routes":null}`} {
		request := httptest.NewRequest("PUT", path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+pro.key)
		rec := httptest.NewRecorder()
		pro.s.handler().ServeHTTP(rec, request)
		if rec.Code != 400 {
			t.Fatalf("input accepted %d %s", rec.Code, rec.Body)
		}
	}
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	for _, suffix := range []string{"", "/report", "/incidents", "/incidents/" + uuid.NewString()} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/apps/mfa-app/route-monitor"+suffix, nil)
		r.AddCookie(pending)
		mfa.h.ServeHTTP(rec, r)
		if rec.Code != 403 {
			t.Fatalf("MFA bypass %s %d", suffix, rec.Code)
		}
	}
}
func TestProductionRouteMonitorWebhookSubscription(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "production-route-hooks")
	req := webhookReq()
	req.EventFilter = []string{"routes.monitor.violated", "routes.monitor.recovered"}
	hook := mustCreateWebhook(t, e, slug, req)
	if len(hook.EventFilter) != 2 {
		t.Fatal("production route transition subscriptions lost")
	}
}
