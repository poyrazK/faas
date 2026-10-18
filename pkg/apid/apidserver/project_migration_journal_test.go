package apidserver_test

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func journalJSON(t *testing.T, h http.Handler, key, method, path string, in any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	return out
}
func TestProjectMigrationJournalMultiServiceReplayAndTargetedRetry(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	archive := journalComposeArchive(t)
	*(*e.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(archive)}
	scan := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "shop", Migration: true, Only: []string{"api", "web"}})
	if scan.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", scan.Code, scan.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(scan.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	in := api.CreateProjectMigrationRequest{ID: uuid.NewString(), PlanToken: plan.PlanToken, Only: []string{"api", "web"}, InitialSecrets: api.ProjectInitialSecrets{"api": {"API_KEY": "api-request-only"}, "web": {"WEB_KEY": "web-request-only"}}}
	created := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations", in)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s; reviewed blockers: %v", created.Code, created.Body, plan.CanApplyReasons)
	}
	var run api.ProjectMigration
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Services) != 2 {
		t.Fatalf("services: %+v", run)
	}
	for _, s := range run.Services {
		if s.App != "shop-"+s.Name {
			t.Fatalf("lost service mapping: %+v", s)
		}
		app, err := (*e.Store()).AppByID(t.Context(), s.AppID)
		if err != nil || app.WorkloadName != s.Name || app.Slug != s.App {
			t.Fatalf("wrong app identity: %+v %v", app, err)
		}
		if s.DeploymentID == "" || s.BuildID == "" || s.Status != "building" {
			t.Fatalf("missing accepted deployment: %+v", s)
		}
		dep, err := (*e.Store()).DeploymentByID(t.Context(), s.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		// Both services' configuration precedes even the first deployment.
		for _, target := range run.Services {
			rows, err := (*e.Store()).ListAllAppSecrets(t.Context(), *e.AcctID(), target.AppID)
			if err != nil || len(rows) != 1 || rows[0].CreatedAt.After(dep.CreatedAt) {
				t.Fatal("configuration was not sealed before all builds")
			}
		}
	}
	replay := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations", in)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	if bytes.Contains(replay.Body.Bytes(), []byte("request-only")) {
		t.Fatal("secret value in receipt")
	}
	if _, err := (*e.Store()).SetDeploymentFailed(t.Context(), run.Services[0].DeploymentID, "test", "failed"); err != nil {
		t.Fatal(err)
	}
	resumed := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations/"+in.ID+"/resume", api.ResumeProjectMigrationRequest{PlanToken: plan.PlanToken, ExpectedVersion: run.Version, Only: []string{"api"}})
	if resumed.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", resumed.Code, resumed.Body)
	}
	var after api.ProjectMigration
	if err := json.Unmarshal(resumed.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Services[0].Attempt != 2 || after.Services[0].DeploymentID == run.Services[0].DeploymentID || after.Services[1].DeploymentID != run.Services[1].DeploymentID || after.Services[1].Status != string(state.DeployBuilding) {
		t.Fatalf("retry did not preserve healthy service: %+v", after.Services)
	}
}

func journalComposeArchive(t *testing.T, extra ...map[string]string) []byte {
	t.Helper()
	compose := `services:
  api:
    build: ./api
    ports: ["8080:8080"]
  web:
    build: ./web
    ports: ["3000:3000"]
    depends_on: [api]
`
	entries := []tar.Header{}
	bodies := map[string][]byte{}
	files := map[string]string{"repo/compose.yaml": compose, "repo/api/Dockerfile": "FROM alpine\nEXPOSE 8080\n", "repo/web/Dockerfile": "FROM alpine\nEXPOSE 3000\n"}
	for _, added := range extra {
		for name, body := range added {
			files[name] = body
		}
	}
	for name, body := range files {
		entries = append(entries, tar.Header{Name: name, Mode: 0644, Size: int64(len(body))})
		bodies[name] = []byte(body)
	}
	return apidserver.BuildTestTarGz(t, entries, bodies)
}

func TestProjectMigrationNamingReviewRejectsCollisionsAndPreservesCustomNames(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	archive := journalComposeArchive(t)
	scan := func(names map[string]string, migration bool) api.PlanResponse {
		t.Helper()
		*(*e.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(archive)}
		response := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "shop", Migration: migration, AppSlugs: names, Only: []string{"api", "web"}})
		if response.Code != http.StatusOK {
			t.Fatalf("scan: %d %s", response.Code, response.Body)
		}
		var plan api.PlanResponse
		if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	for _, names := range []map[string]string{{"api": "api"}, {"api": "existing"}, {"api": "shop-web"}, {"api": "Bad Slug"}} {
		plan := scan(names, true)
		if plan.CanApply || len(plan.CanApplyReasons) == 0 {
			t.Fatalf("accepted blocked names %v: %+v", names, plan)
		}
		response := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations", api.CreateProjectMigrationRequest{ID: uuid.NewString(), PlanToken: plan.PlanToken, Only: []string{"api", "web"}})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("blocked apply: %d %s", response.Code, response.Body)
		}
	}
	names := map[string]string{"api": "shop-backend", "web": "shop-frontend"}
	plan := scan(names, true)
	// A subset review retains only its selected allocations in the token.
	*(*e.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(archive)}
	subset := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "shop", Migration: true, Only: []string{"api"}, AppSlugs: map[string]string{"api": names["api"]}})
	var subsetPlan api.PlanResponse
	if err := json.Unmarshal(subset.Body.Bytes(), &subsetPlan); err != nil {
		t.Fatal(err)
	}
	rawToken, err := base64.StdEncoding.DecodeString(subsetPlan.PlanToken)
	if err != nil {
		t.Fatal(err)
	}
	var token struct {
		AppSlugs map[string]string `json:"app_slugs"`
	}
	if err := json.Unmarshal(rawToken, &token); err != nil {
		t.Fatal(err)
	}
	if subset.Code != http.StatusOK || len(token.AppSlugs) != 1 || token.AppSlugs["api"] != names["api"] {
		t.Fatalf("subset review included unselected allocations: %d %s", subset.Code, rawToken)
	}
	response := journalJSON(t, *e.H(), *e.Key(), http.MethodPost, "/v1/project-migrations", api.CreateProjectMigrationRequest{ID: uuid.NewString(), PlanToken: plan.PlanToken, Only: []string{"api", "web"}})
	if response.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", response.Code, response.Body)
	}
	var record api.ProjectMigration
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for _, service := range record.Services {
		if service.App != names[service.Name] {
			t.Fatalf("lost custom allocation: %+v", service)
		}
	}
	// A normal later source scan also retains the original allocation.
	later := scan(nil, false)
	if !later.CanApply {
		t.Fatal(later.CanApplyReasons)
	}
	for _, w := range later.Workloads {
		if w.AppSlug != names[w.Name] || w.Action != "update" {
			t.Fatalf("normal scan lost mapping: %+v", w)
		}
	}
	if renamed := scan(map[string]string{"api": "replacement"}, true); renamed.CanApply {
		t.Fatal("existing project member renamed")
	}
}
