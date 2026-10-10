package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestScenarioTestRegistrationRequiresSameRunAndCleanup(t *testing.T) {
	e := setup(t, api.PlanPro)
	const runID = "0123456789abcdef0123456789abcdef"
	create := func(project string, workspaceID string) api.DevSessionResponse {
		t.Helper()
		rec := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{WorkspaceID: workspaceID}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", project, rec.Code, rec.Body.String())
		}
		var session api.DevSessionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		return session
	}
	apiSession := create("export-api", runID)
	worker := create("export-api-worker", runID)
	otherRun := create("other-run", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	path := "/v1/dev/test-runs/" + runID
	bad := e.do(t, "PUT", path, api.RegisterScenarioTestRequest{Members: []api.ScenarioTestWorkload{
		{Workload: "api", AppSlug: apiSession.App.Slug}, {Workload: "worker", AppSlug: otherRun.App.Slug},
	}}, nil)
	if bad.Code != http.StatusNotFound {
		t.Fatalf("cross-run registration: %d %s", bad.Code, bad.Body.String())
	}
	good := e.do(t, "PUT", path, api.RegisterScenarioTestRequest{Members: []api.ScenarioTestWorkload{
		{Workload: "api", AppSlug: apiSession.App.Slug}, {Workload: "worker", AppSlug: worker.App.Slug},
	}}, nil)
	if good.Code != http.StatusNoContent {
		t.Fatalf("register: %d %s", good.Code, good.Body.String())
	}
	chaosPath := path + "/chaos"
	installed := e.do(t, "PUT", chaosPath, api.InjectScenarioTestChaosRequest{
		DurationMS: 30_000,
		Rules:      []api.ScenarioTestChaosRule{{From: "api", To: "worker", Kind: chaos.KindHTTPStatus, Percent: 10, StatusCode: 503, Seed: 17}},
	}, nil)
	if installed.Code != http.StatusOK {
		t.Fatalf("install chaos plan: %d %s", installed.Code, installed.Body.String())
	}
	var chaosReceipt api.InjectScenarioTestChaosResponse
	if err := json.Unmarshal(installed.Body.Bytes(), &chaosReceipt); err != nil || chaosReceipt.RulesInstalled != 1 || chaosReceipt.ExpiresAt.IsZero() {
		t.Fatalf("chaos receipt = (%+v, %v)", chaosReceipt, err)
	}
	for _, rule := range []api.ScenarioTestChaosRule{
		{Kind: chaos.KindTCPLatency, LatencyMS: 250},
		{Kind: chaos.KindTCPBandwidth, RateKiBPerSecond: 64, Direction: chaos.DirectionDownstream},
		{Kind: chaos.KindTCPTimeout},
		{Kind: chaos.KindTCPReset, ResetAfterMS: 50},
	} {
		rule.From, rule.To, rule.Port, rule.Percent = "api", "worker", 6379, 100
		rec := e.do(t, "PUT", chaosPath, api.InjectScenarioTestChaosRequest{DurationMS: 1000, Rules: []api.ScenarioTestChaosRule{rule}}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("TCP plan %s: %d %s", rule.Kind, rec.Code, rec.Body.String())
		}
	}

	for _, rule := range []api.ScenarioTestChaosRule{
		{Kind: chaos.KindTCPBandwidth, RateKiBPerSecond: -1, Port: 6379},
		{Kind: chaos.KindTCPTimeout, Port: 443},
		{Kind: chaos.KindTCPReset, Port: 6379, Direction: "upstream"},
		{Kind: chaos.KindTCPLatency, Port: 6379, LatencyMS: 30001},
	} {
		rule.To, rule.Percent = "worker", 100
		rec := e.do(t, "PUT", chaosPath, api.InjectScenarioTestChaosRequest{DurationMS: 1000, Rules: []api.ScenarioTestChaosRule{rule}}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid TCP plan accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	invalidPlan := e.do(t, "PUT", chaosPath, api.InjectScenarioTestChaosRequest{
		DurationMS: 30_000,
		Rules:      []api.ScenarioTestChaosRule{{From: "api", To: "production", Kind: chaos.KindHTTPStatus, Percent: 10, StatusCode: 503}},
	}, nil)
	if invalidPlan.Code != http.StatusBadRequest {
		t.Fatalf("unregistered chaos target: %d %s", invalidPlan.Code, invalidPlan.Body.String())
	}
	if rec := e.do(t, "DELETE", path, nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("active deletion status = %d", rec.Code)
	}
	for _, project := range []string{"export-api-worker", "export-api"} {
		if rec := e.do(t, "DELETE", "/v1/dev/sessions/"+project+"?workspace_id="+runID, nil, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("destroy %s: %d %s", project, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "DELETE", path, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("cleanup: %d %s", rec.Code, rec.Body.String())
	}
}
