package main

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteLatencyAPIConfigurationAndUnavailableEvidence(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "route-latency-api")
	path := "/v1/apps/" + slug + "/route-health/gate"
	req := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", CheckLatency: true, MaxP95MS: 300}}}
	save := func() api.RouteHealthGate {
		t.Helper()
		rec := e.do(t, "PUT", path, req, nil)
		var gate api.RouteHealthGate
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &gate) != nil || len(gate.Routes) != 1 || gate.Routes[0] != req.Routes[0] || gate.UpdatedAt == nil {
			t.Fatalf("latency configuration: %d %s", rec.Code, rec.Body)
		}
		return gate
	}
	first := save()
	if first.Revision != 1 {
		t.Fatal("missing initial revision")
	}
	req.ExpectedRevision = &first.Revision
	unchanged := save()
	if unchanged.Revision != first.Revision || !unchanged.UpdatedAt.Equal(*first.UpdatedAt) {
		t.Fatal("identical latency intent reset observation anchor")
	}
	req.Routes[0].MaxP95MS = 500
	changed := save()
	if changed.Revision != 2 || !changed.UpdatedAt.After(*first.UpdatedAt) {
		t.Fatal("budget edit did not establish fresh observation anchor")
	}
	req.ExpectedRevision = &changed.Revision
	req.Routes[0].CheckLatency = false
	changed = save()
	if changed.Revision != 3 || changed.Routes[0].CheckLatency {
		t.Fatal("relative check cannot be independently disabled")
	}
	app, err := e.store.AppBySlug(t.Context(), slug)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate"})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/apps/"+slug+"/route-health/deployments/"+deployment.ID, nil, nil)
	var report api.RouteHealthReport
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil || report.Status != "unknown" || report.MinimumLatencyRequests != 100 || len(report.Routes) != 1 {
		t.Fatalf("unavailable report: %d %s", rec.Code, rec.Body)
	}
	finding := report.Routes[0]
	if finding.CheckLatency || finding.MaxP95MS != 500 || finding.LatencyStatus != "unknown" || finding.Windows[0].LatencyReason != "telemetry_unavailable" || finding.Windows[0].Candidate.P95LatencyMS != nil {
		t.Fatal("unavailable telemetry invented latency evidence or lost intent")
	}
}

func TestRouteLatencyAPIRejectsInvalidIntent(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "route-latency-validation")
	path := "/v1/apps/" + slug + "/route-health/gate"
	for _, routes := range [][]api.RouteHealthRoute{
		{{Method: "POST", Path: "/checkout", MaxP95MS: -1}},
		{{Method: "POST", Path: "/checkout", MaxP95MS: api.RouteHealthMaxP95BudgetMS + 1}},
		{{Method: "POST", Path: "/checkout", MaxP95MS: 300}, {Method: "POST", Path: "/checkout", MaxP95MS: 500}},
	} {
		req := api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: new(int64), Routes: routes}
		if rec := e.do(t, "PUT", path, req, nil); rec.Code != 400 {
			t.Fatalf("invalid intent accepted: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 200 || !json.Valid(rec.Body.Bytes()) {
		t.Fatal("configuration unavailable after invalid intent")
	} else {
		var gate api.RouteHealthGate
		if json.Unmarshal(rec.Body.Bytes(), &gate) != nil || gate.Revision != 0 || len(gate.Routes) != 0 {
			t.Fatal("invalid intent changed saved configuration")
		}
	}
}
