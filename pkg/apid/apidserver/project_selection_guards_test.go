package apidserver_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func selectionProjectSource(names []string, version string) string {
	var b strings.Builder
	b.WriteString("services:\n")
	for _, name := range names {
		b.WriteString("  " + name + ":\n    image: nginx:" + version + "\n    ports: [\"8080:80\"]\n")
	}
	return b.String()
}

func selectionProjectApply(t *testing.T, h http.Handler, key, source string, fields map[string]string) api.ApplyResponse {
	t.Helper()
	fields["project_slug"] = "environment-shop"
	req := scanAuditRegressionRequest(t, key, environmentProjectArchive(t, source), fields)
	req.URL.Path = "/v1/projects"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var response api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, build := range response.Builds {
		if build.Error != "" {
			t.Fatalf("build failed: %+v", build)
		}
	}
	return response
}

func TestProjectOnlyPersistedExcludedServiceDoesNotWiden(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	store := *e.Store()
	first := selectionProjectApply(t, *e.H(), *e.Key(), selectionProjectSource([]string{"alpha", "beta"}, "1.26"), map[string]string{})
	for _, build := range first.Builds {
		if err := store.MarkDeploymentLive(t.Context(), build.DeploymentID); err != nil {
			t.Fatal(err)
		}
	}
	selectionProjectApply(t, *e.H(), *e.Key(), selectionProjectSource([]string{"alpha", "beta"}, "1.26"), map[string]string{"exclude": "alpha", "persist_exclude": "true"})
	persisted, err := store.LookupDeploymentScopeExclusions(t.Context(), e.Acct().ID, first.ProjectID)
	if err != nil || len(persisted) != 1 || persisted[0].Slug != "alpha" {
		t.Fatalf("public persistence fixture: %+v %v", persisted, err)
	}
	req := scanAuditRegressionRequest(t, *e.Key(), environmentProjectArchive(t, selectionProjectSource([]string{"alpha", "beta"}, "1.27")), map[string]string{"project_slug": "environment-shop", "only": "alpha"})
	req.URL.Path = "/v1/projects"
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "project_selection_empty") {
		t.Fatalf("empty --only selection: %d %s", rec.Code, rec.Body)
	}
	for _, build := range first.Builds {
		live, err := state.ResolveProductionDeployment(t.Context(), store, build.AppID)
		if err != nil || live.ID != build.DeploymentID {
			t.Fatalf("empty selection changed serving deployment: %+v %v", live, err)
		}
	}
}

func TestProjectPersistedAppSlugExclusionPreservesService(t *testing.T) {
	for _, omit := range []bool{false, true} {
		t.Run(map[bool]string{false: "updated", true: "deleted"}[omit], func(t *testing.T) {
			spool := t.TempDir()
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
			t.Setenv("FAAS_SPOOL_ROOT", spool)
			e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
			store := *e.Store()
			first := selectionProjectApply(t, *e.H(), *e.Key(), selectionProjectSource([]string{"api", "web"}, "1.26"), map[string]string{})
			var excluded state.App
			for _, build := range first.Builds {
				if err := store.MarkDeploymentLive(t.Context(), build.DeploymentID); err != nil {
					t.Fatal(err)
				}
				app, err := store.AppByID(t.Context(), build.AppID)
				if err != nil {
					t.Fatal(err)
				}
				if app.WorkloadName == "api" {
					excluded = app
				}
			}
			if excluded.ID == "" || excluded.Slug == "api" {
				t.Fatalf("fixture needs prefixed api slug: %+v", excluded)
			}
			selectionProjectApply(t, *e.H(), *e.Key(), selectionProjectSource([]string{"api", "web"}, "1.26"), map[string]string{"exclude": "api", "persist_exclude": "true"})
			persisted, err := store.LookupDeploymentScopeExclusions(t.Context(), e.Acct().ID, first.ProjectID)
			if err != nil || len(persisted) != 1 || persisted[0].Slug != excluded.Slug {
				t.Fatalf("public persistence fixture: %+v %v", persisted, err)
			}
			names := []string{"api", "web"}
			if omit {
				names = []string{"web"}
			}
			response := selectionProjectApply(t, *e.H(), *e.Key(), selectionProjectSource(names, "1.27"), map[string]string{})
			got, err := store.AppByID(t.Context(), excluded.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status == state.AppDeleted {
				t.Fatalf("excluded app was deleted: %+v", got)
			}
			for _, build := range response.Builds {
				if build.AppID == excluded.ID {
					t.Fatalf("excluded app was rebuilt: %+v", build)
				}
			}
			live, err := state.ResolveProductionDeployment(t.Context(), store, excluded.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, build := range first.Builds {
				if build.AppID == excluded.ID && live.ID != build.DeploymentID {
					t.Fatalf("excluded app traffic changed: %+v", live)
				}
			}
		})
	}
}

func TestProjectProtectedApprovalBindsSelectedService(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	store := *e.Store()
	baseline := selectionProjectApply(t, *e.H(), *e.Key(), selectionProjectSource([]string{"alpha", "beta"}, "1.26"), map[string]string{})
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: "staging", Protected: true}); err != nil {
		t.Fatal(err)
	}
	source := selectionProjectSource([]string{"alpha", "beta"}, "1.27")
	archive := environmentProjectArchive(t, source)
	req := scanAuditRegressionRequest(t, *e.Key(), archive, map[string]string{"project_slug": "environment-shop", "environment": "staging", "only": "alpha"})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	var plan api.PlanResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil || len(plan.Workloads) != 1 || plan.Workloads[0].Name != "alpha" {
		t.Fatalf("protected plan: %d %s", rec.Code, rec.Body)
	}
	legacyBytes, err := base64.StdEncoding.DecodeString(plan.PlanToken)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err = json.Unmarshal(legacyBytes, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "selection")
	legacyBytes, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyBody, err := json.Marshal(api.CreateProjectEnvironmentApprovalRequest{PlanToken: base64.StdEncoding.EncodeToString(legacyBytes)})
	if err != nil {
		t.Fatal(err)
	}
	// Only a person's sign-in approves a protected environment (bug hunt H70).
	humanKey := humanLoginKey(t, *e.Store(), (*e.Acct()).ID)
	legacyReq := httptest.NewRequest(http.MethodPost, "/v1/projects/environment-shop/environments/staging/approvals", strings.NewReader(string(legacyBody)))
	legacyReq.Header.Set("Content-Type", "application/json")
	legacyReq.Header.Set("Authorization", "Bearer "+humanKey)
	legacyResponse := httptest.NewRecorder()
	(*e.H()).ServeHTTP(legacyResponse, legacyReq)
	if legacyResponse.Code != http.StatusConflict {
		t.Fatalf("unbound legacy plan approved: %d %s", legacyResponse.Code, legacyResponse.Body)
	}
	body, err := json.Marshal(api.CreateProjectEnvironmentApprovalRequest{PlanToken: plan.PlanToken})
	if err != nil {
		t.Fatal(err)
	}
	ciReq := httptest.NewRequest(http.MethodPost, "/v1/projects/environment-shop/environments/staging/approvals", strings.NewReader(string(body)))
	ciReq.Header.Set("Content-Type", "application/json")
	ciReq.Header.Set("Authorization", "Bearer "+*e.Key())
	ciApproval := httptest.NewRecorder()
	(*e.H()).ServeHTTP(ciApproval, ciReq)
	apidserver.AssertProblem(t, ciApproval, http.StatusForbidden, api.CodeForbidden)
	approveReq := httptest.NewRequest(http.MethodPost, "/v1/projects/environment-shop/environments/staging/approvals", strings.NewReader(string(body)))
	approveReq.Header.Set("Content-Type", "application/json")
	approveReq.Header.Set("Authorization", "Bearer "+humanKey)
	approved := httptest.NewRecorder()
	(*e.H()).ServeHTTP(approved, approveReq)
	var approval api.ProjectEnvironmentApprovalResponse
	if approved.Code != http.StatusCreated || json.Unmarshal(approved.Body.Bytes(), &approval) != nil {
		t.Fatalf("approval: %d %s", approved.Code, approved.Body)
	}
	// Same source, unmodified plan token, and legitimate approval; only selection changes.
	applyReq := scanAuditRegressionRequest(t, *e.Key(), archive, map[string]string{"project_slug": "environment-shop", "environment": "staging", "only": "beta", "plan_token": plan.PlanToken, "approval_token": approval.ApprovalToken})
	applyReq.URL.Path = "/v1/projects"
	applyQuery := applyReq.URL.Query()
	applyQuery.Set("plan_token", plan.PlanToken)
	applyReq.URL.RawQuery = applyQuery.Encode()
	applied := httptest.NewRecorder()
	(*e.H()).ServeHTTP(applied, applyReq)
	if applied.Code != http.StatusConflict || !strings.Contains(applied.Body.String(), "plan_token_stale") {
		t.Fatalf("changed protected selection accepted: %d %s", applied.Code, applied.Body)
	}
	// A rejected selection must leave the approval usable for the reviewed plan.
	// Cached applies that omit filters must inherit the reviewed --only set.
	accepted := migrationJSON(t, *e.H(), *e.Key(), api.ApplyCachedProjectPlanRequest{PlanToken: plan.PlanToken, ApprovalToken: approval.ApprovalToken})
	var result api.ApplyResponse
	if accepted.Code != http.StatusOK || json.Unmarshal(accepted.Body.Bytes(), &result) != nil || len(result.Builds) != 1 || result.Builds[0].Error != "" {
		t.Fatalf("reviewed plan could not reuse approval: %d %s", accepted.Code, accepted.Body)
	}
	app, err := store.AppByID(t.Context(), result.Builds[0].AppID)
	if err != nil || app.WorkloadName != "alpha" {
		t.Fatalf("reviewed workload: %+v %v", app, err)
	}
}

func TestProjectGraphPromotionResumeIgnoresUnrelatedEnvironment(t *testing.T) {
	for _, added := range []string{"none", "production", "staging"} {
		t.Run(added, func(t *testing.T) { testProjectGraphPromotionResumeMembership(t, added) })
	}
}

func testProjectGraphPromotionResumeMembership(t *testing.T, added string) {
	ctx := t.Context()
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	var err error
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"staging", "qa"} {
		if _, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
	}
	deployments := map[string]state.Deployment{}
	graphs := map[string]state.ProjectReleaseSet{}
	for _, scope := range []string{"production", "staging"} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball, SourceSHA256: scope})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+scope, "apps/"+scope, 42); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		graph, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, scope, 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: d.ID}})
		if err != nil {
			t.Fatal(err)
		}
		deployments[scope], graphs[scope] = d, graph
	}
	qa, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "qa-only", WorkloadName: "qa-only", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(qa)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "qa", qa.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	plan, problem := srv.BuildProjectEnvironmentPromotionPlan(ctx, acct, project.Slug, "production", "staging", false)
	if problem != nil || !plan.Preview.CanPromote || !plan.Preview.ReleaseGraphMode || len(plan.Preview.Changes) != 1 {
		t.Fatalf("fresh plan: %+v %v", plan.Preview, problem)
	}
	// Persist the accepted snapshot, then simulate the owner stopping before dispatch.
	_, _, err = store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{
		AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: "production", ToEnvironment: "staging",
		PromotionHash: plan.Preview.PromotionHash, ReleaseGraphMode: true, ReleaseTTLSeconds: 1800, IdempotencyKey: "graph-resume-review",
		SourceReleaseSetID: graphs["production"].ID, PreviousTargetReleaseSetID: graphs["staging"].ID,
		SourceConfigHash: plan.Preview.ConfigDiff.FromHash, PreviousTargetConfigHash: plan.Preview.ConfigDiff.ToHash,
	}, []state.ProjectEnvironmentPromotionWorkload{{WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: deployments["production"].ID, PreviousTargetDeploymentID: deployments["staging"].ID, PreviousTargetTrafficPercent: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if added != "none" {
		newApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "new-member", WorkloadName: "new-member", Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		settings, err := state.WorkloadSettingsFromApp(newApp)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, added, newApp.ID, 0, settings); err != nil {
			t.Fatal(err)
		}
	}
	body, err := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "production", PromotionToken: plan.Preview.PromotionToken})
	if err != nil {
		t.Fatal(err)
	}
	req, rec := apidserver.ProjectRequest(http.MethodPost, "/promote", project.Slug, body)
	req.SetPathValue("environment", "staging")
	req.Header.Set("Idempotency-Key", "graph-resume-review")
	srv.PromoteProjectEnvironment(rec, req, acct)
	if added != "none" {
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "project workloads changed after the promotion started") {
			t.Fatalf("new relevant member accepted: %d %s", rec.Code, rec.Body)
		}
		return
	}
	if rec.Code != http.StatusOK && rec.Code != http.StatusAccepted {
		t.Fatalf("unchanged graph promotion did not resume: %d %s", rec.Code, rec.Body)
	}
	qaDeployments, err := store.LiveDeployments(ctx, qa.ID)
	if err != nil || len(qaDeployments) != 0 {
		t.Fatalf("resume touched unrelated environment: %+v %v", qaDeployments, err)
	}
}
