package apidserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionControlAPI(t *testing.T) {
	srv, store, account, project, app, baseline := newPromotionHealthAPIFixture(t)
	srv.WithPromotionHealthEnabled(true)
	srv.routeProbes = promotionHealthAPISender{}
	gate, err := store.GetRouteHealthGate(t.Context(), account.ID, app.ID)
	mustSnapshotPromotionAPI(t, err)
	_, err = store.SetRouteHealthGate(t.Context(), account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
	mustSnapshotPromotionAPI(t, err)
	plan, problem := srv.buildProjectEnvironmentPromotionPlan(t.Context(), account, project.Slug, "production", "staging", true, true)
	if problem != nil || !plan.Preview.CanPromote {
		t.Fatalf("preview: %v %+v", problem, plan.Preview)
	}
	body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
	req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
	req.SetPathValue("environment", "staging")
	req.Header.Set("Idempotency-Key", "control-api")
	srv.promoteProjectEnvironment(rec, req, account)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admission: %d %s", rec.Code, rec.Body)
	}
	var admitted api.ProjectEnvironmentPromotionResponse
	mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &admitted))
	h, err := store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, admitted.PromotionID)
	mustSnapshotPromotionAPI(t, err)
	request := api.ProjectPromotionControlRequest{RequestID: strings.Repeat("a", 32), ExpectedGeneration: h.Generation}
	for range 2 {
		raw, _ := json.Marshal(request)
		req, rec = projectRequest(http.MethodPost, "/abort", project.Slug, raw)
		req.SetPathValue("environment", "staging")
		req.SetPathValue("promotion", admitted.PromotionID)
		req.SetPathValue("action", "abort")
		srv.controlProjectPromotion(rec, req, account)
		if rec.Code != http.StatusOK {
			t.Fatalf("abort: %d %s", rec.Code, rec.Body)
		}
		var result api.ProjectPromotionControl
		mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &result))
		if result.Status != "aborted" || result.Request != request {
			t.Fatal(result)
		}
	}
	active, err := store.ActiveProjectReleaseSet(t.Context(), account.ID, project.ID, "staging")
	mustSnapshotPromotionAPI(t, err)
	if active.ID != baseline.ID {
		t.Fatal("early abort changed destination")
	}
	h, err = store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, admitted.PromotionID)
	mustSnapshotPromotionAPI(t, err)
	if h.Phase != "aborted" || h.Generation != request.ExpectedGeneration+1 {
		t.Fatal("lost generation fence", h)
	}
}
