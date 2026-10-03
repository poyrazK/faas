package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteCheckHistoryAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "history-routes")
	app, _ := e.store.AppBySlug(t.Context(), slug)
	deployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:history", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/" + slug + "/route-requirements"
	zero := int64(0)
	intent := api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "private-history-intent"}}}}
	if rec := e.do(t, "PUT", base, intent, nil); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if err := e.store.UpsertDeploymentOpenAPIDoc(t.Context(), deployment.ID, e.acct.ID, app.ID, []byte(`{"openapi":"3.1.0","paths":{"/health":{"get":{}}}}`), "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	if n, err := e.s.drainAutomaticRouteChecks(t.Context()); err != nil || n != 1 {
		t.Fatalf("check: %d %v", n, err)
	}
	path := base + "/checks/" + deployment.ID + "/history"
	var page api.RouteCheckHistoryPage
	rec := e.do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Entries) != 1 || page.Entries[0].ComparisonStatus != "initial" || strings.Contains(rec.Body.String(), "/health") {
		t.Fatalf("compact history page: %d %s", rec.Code, rec.Body)
	}
	entryPath := path + "/" + page.Entries[0].ID
	var entry api.RouteCheckHistoryEntry
	rec = e.do(t, "GET", entryPath, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &entry) != nil || entry.Check.Report.Status != "satisfied" || entry.Changes.CheckID != entry.ID || strings.Contains(rec.Body.String(), "private-history-intent") {
		t.Fatalf("retained evidence/privacy: %d %s", rec.Code, rec.Body)
	}
	for _, suffix := range []string{"?limit=0", "?limit=11", "?limit=bad", "?before=invalid", "/invalid"} {
		if rec := e.do(t, "GET", path+suffix, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid history lookup %s: %d", suffix, rec.Code)
		}
	}
	for _, suffix := range []string{"/" + uuid.NewString(), "?before=" + uuid.NewString()} {
		if rec := e.do(t, "GET", path+suffix, nil, nil); rec.Code != 404 {
			t.Fatalf("absent history %s: %d", suffix, rec.Code)
		}
	}
	foreign := mustSeedEdgeRuleApp(t, e, "history-foreign")
	if rec := e.do(t, "GET", strings.Replace(entryPath, slug, foreign, 1), nil, nil); rec.Code != 404 {
		t.Fatal("history crossed app ownership")
	}
	for _, read := range []bool{true, false} {
		key, hash, _ := api.GenerateAPIKey()
		scopes := []string{api.ScopeDeployWrite}
		want := 403
		if read {
			scopes, want = []string{api.ScopeAppsRead}, 200
		}
		if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "history-scope", scopes); err != nil {
			t.Fatal(err)
		}
		e.key = key
		for _, target := range []string{path, entryPath} {
			if rec := e.do(t, "GET", target, nil, nil); rec.Code != want {
				t.Fatalf("history read=%v: %d want %d", read, rec.Code, want)
			}
		}
	}
	if err := e.store.UpdateAccountPlan(t.Context(), e.acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	// Restore read scope to check entitlement independently of scope rejection.
	key, hash, _ := api.GenerateAPIKey()
	_, _ = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "history-downgrade", []string{api.ScopeAppsRead})
	e.key = key
	for _, target := range []string{path, entryPath} {
		if rec := e.do(t, "GET", target, nil, nil); rec.Code != 402 || strings.Contains(rec.Body.String(), "/health") {
			t.Fatalf("history entitlement/privacy: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestRouteCheckHistoryMFA(t *testing.T) {
	mfa := setupWithMFA(t, api.PlanPro, false, false)
	mfa.generateEnrolledAccount(t)
	pending := mfa.mfaIssueWithPending(t, true)
	path := "/v1/apps/hidden/route-requirements/checks/" + uuid.NewString() + "/history"
	for _, target := range []string{path, path + "/" + uuid.NewString()} {
		req := httptest.NewRequest("GET", target, nil)
		req.AddCookie(pending)
		rec := httptest.NewRecorder()
		mfa.h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("history MFA bypass: %d", rec.Code)
		}
	}
}
