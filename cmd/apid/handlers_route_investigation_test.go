package main

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteInvestigationAPISelectionAndUnavailableEvidence(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "investigation")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	d, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:investigation"})
	if err != nil {
		t.Fatal(err)
	}
	req := api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", WatchStatuses: []int{403}}}}
	if rec := e.do(t, "PUT", "/v1/apps/"+slug+"/route-health/gate", req, nil); rec.Code != 200 {
		t.Fatalf("configuration: %d %s", rec.Code, rec.Body)
	}
	path := "/v1/apps/" + slug + "/route-health/deployments/" + d.ID + "/investigation"
	for _, query := range []string{"?method=POST&path=%2Fcheckout", "?method=POST&path=%2Fcheckout&status_code=403"} {
		rec := e.do(t, "GET", path+query, nil, nil)
		var report api.RouteHealthInvestigation
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil || report.Status != "unknown" || report.EvidenceStatus != "unavailable" || len(report.Windows) != 2 || report.Windows[0].Candidate.MatchingRequests != 0 || len(report.Windows[0].Candidate.Examples) != 0 {
			t.Fatalf("explicit missing evidence: %d %s", rec.Code, rec.Body)
		}
	}
	for _, query := range []string{"", "?method=POST", "?method=POST&path=%2Fcheckout&method=GET", "?method=POST&path=%2Fcheckout&status_code=500", "?method=POST&path=%2Fcheckout&status_code=404", "?method=POST&path=%2Fcheckout&customer_group_by=tenant", "?method=POST&path=%2Fcheckout&customer_id=broken", "?method=POST&path=%2Fcheckout&since=1h", "?method=GET&path=%2Fcheckout"} {
		if rec := e.do(t, "GET", path+query, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid selection %s: %d %s", query, rec.Code, rec.Body)
		}
	}
	for _, target := range []string{"/v1/apps/hidden/route-health/deployments/" + d.ID + "/investigation", "/v1/apps/" + slug + "/route-health/deployments/" + uuid.NewString() + "/investigation"} {
		if rec := e.do(t, "GET", target+"?method=POST&path=%2Fcheckout", nil, nil); rec.Code != 404 {
			t.Fatalf("scope: %d %s", rec.Code, rec.Body)
		}
	}
	for _, test := range []struct {
		scope  string
		status int
	}{{api.ScopeAppsRead, 200}, {api.ScopeDeployWrite, 403}} {
		key, hash, _ := api.GenerateAPIKey()
		if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "investigate", []string{test.scope}); err != nil {
			t.Fatal(err)
		}
		e.key = key
		if rec := e.do(t, "GET", path+"?method=POST&path=%2Fcheckout", nil, nil); rec.Code != test.status {
			t.Fatalf("read scope: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestRouteInvestigationAPIPlanGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	slug := mustSeedEdgeRuleApp(t, e, "investigation-free")
	path := "/v1/apps/" + slug + "/route-health/deployments/" + uuid.NewString() + "/investigation?method=POST&path=%2Fcheckout"
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 402 {
		t.Fatalf("plan gate: %d %s", rec.Code, rec.Body)
	}
}
