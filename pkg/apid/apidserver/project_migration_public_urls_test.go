package apidserver_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestProjectMigrationPublicURLsCapturedAndRestoredOnPartialRetry(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	archive := journalComposeArchive(t, map[string]string{"repo/web/.gregale/required-secrets.json": `["VITE_API_URL"]`})
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
	if plan.Workloads[0].PublicURL != "https://shop-api.gregale.dev" {
		t.Fatal("reviewed public endpoint missing", plan.Workloads[0].PublicURL)
	}
	in := api.CreateProjectMigrationRequest{ID: uuid.NewString(), PlanToken: plan.PlanToken, Only: []string{"api", "web"},
		InitialSecrets:           api.ProjectInitialSecrets{"api": {"DATABASE_URL": "private-database-value"}},
		PublicServiceConnections: api.ProjectServiceConnections{"web": {"VITE_API_URL": "api"}},
		PublicBuildURLs:          api.ProjectPublicBuildURLs{"web": {"NEXT_PUBLIC_AUTH_URL": "https://auth.example.com/v1"}},
		BuildDependencies:        map[string][]string{"web": {"api"}},
	}
	rec := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations", in)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var saved api.ProjectMigration
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("private-database-value")) {
		t.Fatal("secret disclosed")
	}
	web := saved.Services[1]
	if web.Status != "waiting" || len(web.BuildDependencies) != 1 || web.BuildWait == nil {
		t.Fatalf("build wait not saved: %+v", web)
	}
	claim, err := (*e.Store()).ClaimQueuedBuild(t.Context(), web.BuildID)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := (*e.Store()).CheckBuildDependencies(t.Context(), claim, time.Now().Add(-api.ProjectMigrationBuildWaitPoll-time.Second))
	if err != nil || gate.Status != "waiting" || gate.Dependencies[0].DeploymentID != saved.Services[0].DeploymentID {
		t.Fatalf("queued build lost selected API: %+v %v", gate, err)
	}
	if web.PublicBuildURLs["VITE_API_URL"] != plan.Workloads[0].PublicURL || web.PublicServiceConnections["VITE_API_URL"] != "api" {
		t.Fatal("public choices missing")
	}
	urls, err := (*e.Store()).DeploymentPublicBuildURLs(t.Context(), web.DeploymentID)
	if err != nil || urls["VITE_API_URL"] != plan.Workloads[0].PublicURL || urls["NEXT_PUBLIC_AUTH_URL"] != "https://auth.example.com/v1" {
		t.Fatal("queued build missed public inputs", err)
	}
	secrets, err := (*e.Store()).ListAllAppSecrets(t.Context(), *e.AcctID(), web.AppID)
	if err != nil || len(secrets) != 0 {
		t.Fatal("public values entered secret storage", err)
	}
	if err := (*e.Store()).SetAppPublicBuildURLs(t.Context(), *e.AcctID(), web.AppID, map[string]string{"VITE_API_URL": "https://changed.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).SetDeploymentFailed(t.Context(), web.DeploymentID, "fixture", "failed"); err != nil {
		t.Fatal(err)
	}
	review := scan([]string{"web"})
	resume := api.ResumeProjectMigrationRequest{ExpectedVersion: saved.Version, Only: []string{"web"}, PlanToken: review.PlanToken}
	resume.InitialSecrets = api.ProjectInitialSecrets{"web": {"VITE_API_URL": "private-replacement"}}
	bad := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations/"+saved.ID+"/resume", resume)
	if bad.Code != http.StatusBadRequest || bytes.Contains(bad.Body.Bytes(), []byte("private-replacement")) {
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
	if after.Services[0].DeploymentID != saved.Services[0].DeploymentID || after.Services[1].Attempt != 2 {
		t.Fatal("partial retry changed accepted sibling")
	}
	dep, err := (*e.Store()).DeploymentByID(t.Context(), after.Services[1].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	var stages state.StageState
	if err := json.Unmarshal(dep.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if stages.BuildDependencyGate == nil || stages.BuildDependencyGate.Dependencies[0].DeploymentID != saved.Services[0].DeploymentID {
		t.Fatal("partial retry changed API target")
	}
	urls, err = (*e.Store()).DeploymentPublicBuildURLs(t.Context(), after.Services[1].DeploymentID)
	if err != nil || urls["VITE_API_URL"] != plan.Workloads[0].PublicURL {
		t.Fatal("retry did not restore reviewed public URL", err)
	}
}
