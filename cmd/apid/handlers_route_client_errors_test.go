package main

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWatchedClientErrorAPISelectorsAndUnavailableEvidence(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "watched-status")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	d, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:watched"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + slug + "/route-health"
	req := api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", WatchStatuses: []int{403, 422}}}}
	rec := e.do(t, "PUT", path+"/gate", req, nil)
	if rec.Code != 200 {
		t.Fatalf("%d: %s", rec.Code, rec.Body)
	}
	var gate api.RouteHealthGate
	if err := json.Unmarshal(rec.Body.Bytes(), &gate); err != nil || len(gate.Routes[0].WatchStatuses) != 2 {
		t.Fatal("intent roundtrip")
	}
	for _, query := range []string{"", "?customers=true"} {
		rec := e.do(t, "GET", path+"/deployments/"+d.ID+query, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%d: %s", rec.Code, rec.Body)
		}
		var report api.RouteHealthReport
		if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.ClientErrorStatus != "unknown" || report.Routes[0].ClientErrors.Status != "unknown" || len(report.Routes[0].ClientErrors.Statuses) != 2 {
			t.Fatal("unavailable evidence was hidden")
		}
	}
	for _, codes := range [][]int{{403, 403}, {400}, {500}, {200}, {401, 403, 404, 422, 429, 401}} {
		req.Routes[0].WatchStatuses = codes
		if rec := e.do(t, "PUT", path+"/gate", req, nil); rec.Code != 400 {
			t.Fatalf("invalid %v: %d", codes, rec.Code)
		}
	}
}
