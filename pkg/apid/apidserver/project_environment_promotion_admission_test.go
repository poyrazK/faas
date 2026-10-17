package apidserver

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"strings"
	"testing"
)

func TestPromotionAdmissionAPI(t *testing.T) {
	srv, store, account, project, app, _ := newPromotionHealthAPIFixture(t)
	srv.WithPromotionHealthEnabled(true)
	srv.routeProbes = promotionHealthAPISender{}
	gate, err := store.GetRouteHealthGate(t.Context(), account.ID, app.ID)
	mustSnapshotPromotionAPI(t, err)
	_, err = store.SetRouteHealthGate(t.Context(), account.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &gate.Revision, Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/database-health", Probe: &api.RouteHealthProbe{Path: "/database-health"}}}})
	mustSnapshotPromotionAPI(t, err)
	plan, problem := srv.buildProjectEnvironmentPromotionPlan(t.Context(), account, project.Slug, "production", "staging", true, true)
	if problem != nil || !plan.Preview.CanPromote {
		t.Fatal("preview", problem, plan.Preview)
	}
	body, _ := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
	var firstQueued string
	for i := range api.ProjectPromotionAdmissionAccountActive + api.ProjectPromotionAdmissionAccountQueued + 1 {
		req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
		req.SetPathValue("environment", "staging")
		req.Header.Set("Idempotency-Key", fmt.Sprintf("admission-%d", i))
		srv.promoteProjectEnvironment(rec, req, account)
		if i == api.ProjectPromotionAdmissionAccountActive+api.ProjectPromotionAdmissionAccountQueued {
			if rec.Code != 429 || rec.Header().Get("Retry-After") != "5" || !strings.Contains(rec.Body.String(), api.CodeProjectPromotionQueueFull) {
				t.Fatal("full queue response", rec.Code, rec.Body)
			}
			continue
		}
		if rec.Code != 202 {
			t.Fatal("acceptance", i, rec.Code, rec.Body)
		}
		var out api.ProjectEnvironmentPromotionResponse
		mustSnapshotPromotionAPI(t, json.Unmarshal(rec.Body.Bytes(), &out))
		h, err := store.ProjectEnvironmentPromotionHealth(t.Context(), account.ID, out.PromotionID)
		mustSnapshotPromotionAPI(t, err)
		if i >= api.ProjectPromotionAdmissionAccountActive {
			if h.Phase != "queued" || h.Admission == nil {
				t.Fatal("queue hidden", h)
			}
			if firstQueued == "" {
				firstQueued = out.PromotionID
			}
		}
	}
	// A full queue must still replay a previously accepted idempotency key.
	req, rec := projectRequest(http.MethodPost, "/promote", project.Slug, body)
	req.SetPathValue("environment", "staging")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("admission-%d", api.ProjectPromotionAdmissionAccountActive))
	srv.promoteProjectEnvironment(rec, req, account)
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), firstQueued) {
		t.Fatal("accepted request could not resume", rec.Code, rec.Body)
	}
	req, rec = projectRequest(http.MethodGet, "/status", project.Slug, nil)
	req.SetPathValue("environment", "staging")
	req.SetPathValue("promotion", firstQueued)
	srv.getProjectEnvironmentPromotionStatus(rec, req, account)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"phase":"queued"`) || !strings.Contains(rec.Body.String(), `"wait_deadline"`) {
		t.Fatal("missing durable queue status", rec.Code, rec.Body)
	}
}
