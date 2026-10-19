package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectMigrationConnectionsConfigureBeforeBuildAndResumeSavedIntent(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	archive := journalComposeArchive(t, map[string]string{"repo/web/.gregale/required-secrets.json": `["API_URL"]`})
	scan := func(only []string) api.PlanResponse {
		t.Helper()
		*(*e.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(archive)}
		rec := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "shop", Migration: true, Only: only})
		if rec.Code != http.StatusOK {
			t.Fatalf("scan: %d %s", rec.Code, rec.Body)
		}
		var plan api.PlanResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	plan := scan([]string{"api", "web"})
	in := api.CreateProjectMigrationRequest{ID: uuid.NewString(), PlanToken: plan.PlanToken, Only: []string{"api", "web"}, ServiceConnections: api.ProjectServiceConnections{"web": {"API_URL": "api"}}}
	rec := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations", in)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var saved api.ProjectMigration
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Services[1].ServiceConnections["API_URL"] != "api" || !slices.Contains(saved.Services[1].ConfiguredKeys, "API_URL") {
		t.Fatal("connection intent lost")
	}
	if saved.Services[0].ConfiguredKeys == nil {
		t.Fatal("empty configuration keys must remain a JSON array")
	}
	rows, err := (*e.Store()).ListAllAppSecrets(t.Context(), *e.AcctID(), saved.Services[1].AppID)
	if err != nil || len(rows) != 1 || rows[0].Key != "API_URL" {
		t.Fatalf("mapped configuration not sealed: %d %v", len(rows), err)
	}
	for _, s := range saved.Services {
		d, err := (*e.Store()).DeploymentByID(t.Context(), s.DeploymentID)
		if err != nil || rows[0].CreatedAt.After(d.CreatedAt) {
			t.Fatal("build preceded connection configuration")
		}
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("https://api.internal")) {
		t.Fatal("resolved URL entered the receipt")
	}
	if _, err := (*e.Store()).SetDeploymentFailed(t.Context(), saved.Services[1].DeploymentID, "test", "failed"); err != nil {
		t.Fatal(err)
	}
	review := scan([]string{"web"})
	if !review.CanApply {
		t.Fatalf("partial review blockers: %v", review.CanApplyReasons)
	}
	resume := api.ResumeProjectMigrationRequest{PlanToken: review.PlanToken, ExpectedVersion: saved.Version, Only: []string{"web"}}
	// An override must fail before claiming a lease or incrementing attempts.
	resume.InitialSecrets = api.ProjectInitialSecrets{"web": {"API_URL": "private-override"}}
	bad := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations/"+saved.ID+"/resume", resume)
	if bad.Code != http.StatusBadRequest || bytes.Contains(bad.Body.Bytes(), []byte("private-override")) {
		t.Fatalf("override: %d %s", bad.Code, bad.Body)
	}
	resume.InitialSecrets = nil
	again := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations/"+saved.ID+"/resume", resume)
	if again.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", again.Code, again.Body)
	}
	var after api.ProjectMigration
	if err := json.Unmarshal(again.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Services[0].DeploymentID != saved.Services[0].DeploymentID || after.Services[1].Attempt != 2 || after.Services[1].ServiceConnections["API_URL"] != "api" {
		t.Fatal("retry changed accepted sibling or lost mapping")
	}
	app, err := (*e.Store()).AppByID(t.Context(), after.Services[1].AppID)
	if err != nil || !slices.ContainsFunc(app.Manifest.ServiceBindings, func(b api.AppServiceBinding) bool { return b.Service == "api" }) || app.Status == state.AppDeleted {
		t.Fatal("declared binding lost on subset retry")
	}
}

func TestProjectMigrationKeepsUnselectedDependencyOnCurrentProvider(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	*(*e.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(journalComposeArchive(t))}
	review := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "shop", Migration: true, Only: []string{"web"}})
	var plan api.PlanResponse
	if err := json.Unmarshal(review.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if review.Code != http.StatusOK || !plan.CanApply || len(plan.Workloads) != 1 {
		t.Fatalf("partial migration review: %d %s", review.Code, review.Body)
	}
	rec := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations", api.CreateProjectMigrationRequest{ID: uuid.NewString(), PlanToken: plan.PlanToken, Only: []string{"web"}, InitialSecrets: api.ProjectInitialSecrets{"web": {"API_URL": "https://private-existing.example"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var run api.ProjectMigration
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	apps, err := (*e.Store()).AppsForProject(t.Context(), *e.AcctID(), run.ProjectID)
	if err != nil || len(apps) != 1 || apps[0].WorkloadName != "web" || len(apps[0].Manifest.ServiceBindings) != 0 {
		t.Fatal("unselected provider dependency gained an app or binding")
	}
	if len(run.Services[0].ServiceConnections) != 0 || bytes.Contains(rec.Body.Bytes(), []byte("private-existing")) {
		t.Fatal("external configuration was mapped or disclosed")
	}
}
