package main

// adr: 458

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type routeRecoveryAPIStore struct {
	*state.MemStore
	account, app, deployment string
	calls                    int
}

func (s *routeRecoveryAPIStore) RecoverCanaryRouteHealth(_ context.Context, account, app, deployment string, step int) (state.RouteHealthRecoveryResult, error) {
	s.calls++
	if account != s.account || app != s.app || deployment != s.deployment || step != 0 {
		return state.RouteHealthRecoveryResult{}, state.ErrNotFound
	}
	if s.calls == 1 {
		return state.RouteHealthRecoveryResult{Decision: &api.RouteHealthDecision{DeploymentID: deployment, Status: "allowed"}}, nil
	}
	return state.RouteHealthRecoveryResult{Aborted: true, AuditID: 42, Decision: &api.RouteHealthDecision{DeploymentID: deployment, Mode: "enforce", OnRegression: "abort", Status: "aborted", HistoryID: uuid.NewString()}}, nil
}
func TestRouteHealthRecoveryAPIActionIsolationAndFreshChecks(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "health-abort")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	stable, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	d, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", CanaryTotalSteps: 4, CanaryPreset: "balanced", RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	path := "/v1/internal/safe-deploy/deployments/" + d.ID + "/route-health/recover"
	if rec := e.do(t, "POST", path, api.CanaryRouteHealthRecoveryRequest{ExpectedStep: 0}, nil); rec.Code != 404 {
		t.Fatal("private action exposed publicly", rec.Code)
	}
	const canaryToken = "canary-service-secret-0000000000000001"
	const actionToken = "action-service-secret-0000000000000001"
	mux := http.NewServeMux()
	if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", canaryToken, actionToken); err != nil {
		t.Fatal(err)
	}
	call := func(token, remote, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.RemoteAddr = remote
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "same-check-key")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	for _, tc := range []struct {
		token, remote string
		status        int
	}{{e.key, "127.0.0.1:1234", 401}, {canaryToken, "127.0.0.1:1234", 401}, {actionToken, "192.0.2.1:1234", 403}} {
		if rec := call(tc.token, tc.remote, `{"expected_step":0}`); rec.Code != tc.status {
			t.Fatal("action authorization", rec.Code, tc.status)
		}
	}
	if rec := call(actionToken, "127.0.0.1:1234", `{"expected_step":-1}`); rec.Code != 400 {
		t.Fatal("negative stage accepted", rec.Code)
	}
	if rec := call(actionToken, "127.0.0.1:1234", `{"expected_step":0}`); rec.Code != 503 {
		t.Fatal("missing lease accepted", rec.Code, rec.Body)
	}
	if err := e.store.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if rec := call(actionToken, "127.0.0.1:1234", `{"expected_step":1}`); rec.Code != 409 {
		t.Fatal("stale stage accepted", rec.Code, rec.Body)
	}
	gate := api.SetRouteHealthGateRequest{Mode: "enforce", OnRegression: "abort", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout"}}}
	if rec := e.do(t, "PUT", "/v1/apps/"+slug+"/route-health/gate", gate, nil); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	rec := call(actionToken, "127.0.0.1:1234", `{"expected_step":0}`)
	var unknown api.CanaryRouteHealthRecoveryResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &unknown) != nil || unknown.Aborted || unknown.RouteHealth == nil || unknown.RouteHealth.Status != "blocked" {
		t.Fatal("unavailable evidence authorized abort", rec.Code, rec.Body)
	}
	page, err := e.store.ListRouteHealthHistory(t.Context(), e.acct.ID, app.ID, d.ID, 5, "")
	if err != nil || len(page.Entries) != 0 {
		t.Fatal("periodic check retained history", err)
	}
	stub := &routeRecoveryAPIStore{MemStore: e.store, account: e.acct.ID, app: app.ID, deployment: d.ID}
	e.s.store = stub
	for i := 0; i < 2; i++ {
		rec := call(actionToken, "127.0.0.1:1234", `{"expected_step":0}`)
		var result api.CanaryRouteHealthRecoveryResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Aborted != (i == 1) {
			t.Fatal("check response cached", i, rec.Code, rec.Body)
		}
		if result.Aborted && (result.RouteHealth.HistoryID == "" || result.AuditID != "42") {
			t.Fatal("committed provenance lost")
		}
	}
	if stub.calls != 2 {
		t.Fatal("repeated key suppressed fresh check", stub.calls)
	}
}

func TestRouteHealthRecoveryAPIConfigurationDefaultAndSubscription(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "health-action")
	path := "/v1/apps/" + slug + "/route-health/gate"
	for _, tc := range []struct {
		body   map[string]any
		action string
		status int
	}{
		{map[string]any{"mode": "enforce", "on_regression": "delete", "expected_revision": 0, "routes": []map[string]string{{"method": "GET", "path": "/health"}}}, "", 400},
		{map[string]any{"mode": "enforce", "on_regression": "abort", "expected_revision": 0, "routes": []map[string]string{{"method": "GET", "path": "/health"}}}, "abort", 200},
		{map[string]any{"mode": "enforce", "expected_revision": 1, "routes": []map[string]string{{"method": "GET", "path": "/health"}}}, "hold", 200},
	} {
		rec := e.do(t, "PUT", path, tc.body, nil)
		if rec.Code != tc.status {
			t.Fatal("policy update", rec.Code, rec.Body)
		}
		if tc.status == 200 {
			var gate api.RouteHealthGate
			if json.Unmarshal(rec.Body.Bytes(), &gate) != nil || gate.OnRegression != tc.action {
				t.Fatal("action roundtrip", rec.Body)
			}
		}
	}
	req := webhookReq()
	req.EventFilter = []string{"routes.health.aborted"}
	hook := mustCreateWebhook(t, e, slug, req)
	if len(hook.EventFilter) != 1 || hook.EventFilter[0] != "routes.health.aborted" {
		t.Fatal("abort subscription lost")
	}
}
