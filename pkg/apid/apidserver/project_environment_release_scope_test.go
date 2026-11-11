package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

const environmentPostgresSource = "services:\n  web:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n    depends_on: [db]\n  db:\n    image: postgres:17\n"

func TestProjectComposePostgresSeparatesEnvironments(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	databases := apidserver.AttachTestManagedPostgres(t, *e.S(), 4)
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", environmentPostgresSource)
	store := *e.Store()
	scanStage := func(scope string) api.PlanManaged {
		t.Helper()
		req := scanAuditRegressionRequest(t, *e.Key(), environmentProjectArchive(t, environmentPostgresSource), map[string]string{"project_slug": "environment-shop", "environment": scope})
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		var plan api.PlanResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil || len(plan.Managed) != 1 {
			t.Fatalf("stage plan: %d %s", rec.Code, rec.Body)
		}
		return plan.Managed[0]
	}
	createStage := func(scope string) {
		t.Helper()
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
	}
	createStage("staging")
	provision := scanStage("staging").Provision
	if provision == nil || provision.Existing || provision.Database == "environment-shop-db" || !managedpostgres.ValidName(provision.Database) {
		t.Fatalf("stage would reuse production: %+v", provision)
	}
	applyEnvironmentProject(t, *e.H(), *e.Key(), "staging", environmentPostgresSource)
	applyEnvironmentProject(t, *e.H(), *e.Key(), "staging", environmentPostgresSource)
	all, err := databases.List(t.Context(), e.Acct().ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("expected stable database per environment: %d %v", len(all), err)
	}
	for _, database := range all {
		bindings, err := databases.ListBindings(t.Context(), e.Acct().ID, database.ID)
		if err != nil || len(bindings) != 1 {
			t.Fatalf("bindings: %+v %v", bindings, err)
		}
		wantScope := "staging"
		if database.Name == "environment-shop-db" {
			wantScope = api.DefaultEnvScope
		}
		if bindings[0].Scope != wantScope || bindings[0].Access != managedpostgres.CredentialReadWrite {
			t.Fatalf("cross-environment binding: %+v", bindings[0])
		}
	}
	// Truncation must not alias long environment names sharing a prefix.
	longA, longB := strings.Repeat("s", 32)+"a", strings.Repeat("s", 32)+"b"
	createStage(longA)
	createStage(longB)
	a, b := scanStage(longA).Provision, scanStage(longB).Provision
	if a == nil || b == nil || a.Database == b.Database || !managedpostgres.ValidName(a.Database) || !managedpostgres.ValidName(b.Database) {
		t.Fatalf("long stage names collide: %+v %+v", a, b)
	}
}

func TestProjectComposePostgresQuotaNeverFallsBackToProduction(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	databases := apidserver.AttachTestManagedPostgres(t, *e.S(), 1)
	baseline := applyEnvironmentProject(t, *e.H(), *e.Key(), "", environmentPostgresSource)
	if _, err := (*e.Store()).CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.Acct().ID, ProjectID: baseline.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	req := scanAuditRegressionRequest(t, *e.Key(), environmentProjectArchive(t, environmentPostgresSource), map[string]string{"project_slug": "environment-shop", "environment": "staging"})
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	var plan api.PlanResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil || len(plan.Managed) != 1 {
		t.Fatalf("stage plan: %d %s", rec.Code, rec.Body)
	}
	if plan.Managed[0].Provision != nil || plan.Managed[0].NotProvisionedReason != "quota" {
		t.Fatalf("stage reused production on quota exhaustion: %+v", plan.Managed)
	}
	all, err := databases.List(t.Context(), e.Acct().ID)
	if err != nil || len(all) != 1 {
		t.Fatalf("databases: %+v %v", all, err)
	}
}

func TestProjectPromotionIgnoresOtherEnvironmentsAndProtectsTargetMembership(t *testing.T) {
	ctx := t.Context()
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	for _, scope := range []string{"staging", "qa"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
	}
	for i, scope := range []string{"production", "staging"} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat(string(rune('a'+i)), 64)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+scope, "apps/"+scope, 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	extra, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "qa-only-service", WorkloadName: "qa-only", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(extra)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "qa", extra.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	plan, problem := srv.BuildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "staging", "production", false)
	if problem != nil || !plan.Preview.CanPromote || len(plan.Preview.Changes) != 1 {
		t.Fatalf("QA service blocked promotion: %+v %v", plan.Preview, problem)
	}
	// Once it has target membership, it must not be silently removed by promoting a smaller source.
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", extra.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	plan, problem = srv.BuildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "staging", "production", false)
	if problem != nil || plan.Preview.CanPromote || len(plan.Preview.Changes) != 2 {
		t.Fatalf("target-only service was silently omitted: %+v %v", plan.Preview, problem)
	}
}

func TestProjectPromotionRejectsEmptyEnvironments(t *testing.T) {
	ctx := t.Context()
	srv, store, account, project, _ := apidserver.NewProjectLifecycleFixture(t)
	for _, scope := range []string{"staging", "qa"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
	}
	plan, problem := srv.BuildProjectEnvironmentPromotionPlan(ctx, account, project.Slug, "staging", "qa", false)
	if problem != nil || plan.Preview.CanPromote || len(plan.Preview.Changes) != 0 || !slices.Contains(plan.Preview.BlockingReasons, "source environment has no workloads to promote") {
		t.Fatalf("empty environments could be promoted: %+v %v", plan.Preview, problem)
	}
}
