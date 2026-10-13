package apidserver_test

// adr: 456

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteHealthHistoryAPIHeldEvidenceAndAccess(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "health-history-api")
	app, _ := (*e.Store()).AppBySlug(t.Context(), slug)
	for _, candidate := range []bool{false, true} {
		d := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:history"}
		if candidate {
			d.CanaryPreset, d.CanaryTotalSteps, d.RolloutState, d.TrafficPercent = "balanced", 4, "pending", 1
		}
		if _, err := (*e.Store()).CreateDeployment(t.Context(), d); err != nil {
			t.Fatal(err)
		}
		if err := (*e.Store()).MarkDeploymentLive(t.Context(), d.ID); err != nil {
			t.Fatal(err)
		}
		if !candidate {
			continue
		}
		base := "/v1/apps/" + slug + "/route-health"
		path := base + "/deployments/" + d.ID + "/history"
		request := api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}}
		if rec := e.Do(t, "PUT", base+"/gate", request, nil); rec.Code != 200 {
			t.Fatalf("gate %d %s", rec.Code, rec.Body)
		}
		if rec := e.Do(t, "GET", base+"/deployments/"+d.ID, nil, nil); rec.Code != 200 {
			t.Fatal(rec.Code)
		}
		var page api.RouteHealthHistoryPage
		rec := e.Do(t, "GET", path, nil, nil)
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || page.Entries == nil || len(page.Entries) != 0 {
			t.Fatal("read fabricated history", rec.Code)
		}
		rec = e.Do(t, "POST", "/v1/deployments/"+d.ID+"/canary/advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "routes health explain") {
			t.Fatalf("missing explanation hint: %d %s", rec.Code, rec.Body)
		}
		hint := rec.Body.String()
		rec = e.Do(t, "GET", path, nil, nil)
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Entries) != 1 || !strings.Contains(hint, page.Entries[0].ID) {
			t.Fatalf("held evidence %d %s", rec.Code, rec.Body)
		}
		entryPath := path + "/" + page.Entries[0].ID
		var entry api.RouteHealthHistoryEntry
		rec = e.Do(t, "GET", entryPath, nil, nil)
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &entry) != nil || entry.Decision.Status != "blocked" || entry.Report.Routes[0].MaxP95MS != 300 || entry.Policy.MinLatencyRequests != 100 {
			t.Fatal("missing immutable observations")
		}
		for _, suffix := range []string{"?limit=0", "?limit=11", "?limit=bad", "?before=bad", "/bad"} {
			if rec := e.Do(t, "GET", path+suffix, nil, nil); rec.Code != 400 {
				t.Fatalf("validation %s: %d", suffix, rec.Code)
			}
		}
		for _, suffix := range []string{"/" + uuid.NewString(), "?before=" + uuid.NewString()} {
			if rec := e.Do(t, "GET", path+suffix, nil, nil); rec.Code != 404 {
				t.Fatal("absent evidence accepted", rec.Code)
			}
		}
		foreign := apidserver.MustSeedEdgeRuleApp(t, e, "health-history-foreign")
		if rec := e.Do(t, "GET", strings.Replace(entryPath, slug, foreign, 1), nil, nil); rec.Code != 404 {
			t.Fatal("crossed app identity", rec.Code)
		}
		if _, _, err := (*e.Store()).RecoverRollout(t.Context(), app.ID, "abort", ""); err != nil {
			t.Fatal(err)
		}
		if err := (*e.Store()).UpdateAccountPlan(t.Context(), (*e.Acct()).ID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		for _, read := range []bool{false, true} {
			key, hash, _ := api.GenerateAPIKey()
			scopes, want := []string{api.ScopeDeployWrite}, 403
			if read {
				scopes, want = []string{api.ScopeAppsRead}, 200
			}
			if _, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "history", scopes); err != nil {
				t.Fatal(err)
			}
			(*e.Key()) = key
			for _, target := range []string{path, entryPath} {
				if rec := e.Do(t, "GET", target, nil, nil); rec.Code != want {
					t.Fatalf("history after abort/downgrade read=%v: %d %s", read, rec.Code, rec.Body)
				}
			}
		}
		foreignAccount, err := (*e.Store()).CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		key, hash, _ := api.GenerateAPIKey()
		if _, err := (*e.Store()).CreateAPIKey(t.Context(), foreignAccount.ID, hash, "foreign", []string{api.ScopeAppsRead}); err != nil {
			t.Fatal(err)
		}
		(*e.Key()) = key
		if rec := e.Do(t, "GET", entryPath, nil, nil); rec.Code != 404 {
			t.Fatal("crossed account identity", rec.Code)
		}
	}
}

func TestRouteHealthHistoryAPIMFA(t *testing.T) {
	mfa := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	mfa.GenerateEnrolledAccount(t)
	pending := mfa.MfaIssueWithPending(t, true)
	path := "/v1/apps/hidden/route-health/deployments/" + uuid.NewString() + "/history"
	for _, target := range []string{path, path + "/" + uuid.NewString()} {
		req := httptest.NewRequest("GET", target, nil)
		req.AddCookie(pending)
		rec := httptest.NewRecorder()
		(*mfa.H()).ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatal("history bypassed pending MFA", rec.Code)
		}
	}
}
