package apidserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectApplyCronOnlyPartialFailureAndReuse(t *testing.T) {
	s, store, acct, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	apply := func(fields map[string]string) api.ApplyResponse {
		t.Helper()
		rec := projectSecretsRequest(t, s, key, "/v1/projects", "", fields)
		if rec.Code != http.StatusOK {
			t.Fatalf("apply=%d %s", rec.Code, rec.Body.String())
		}
		var result api.ApplyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := apply(nil)
	if len(first.Builds) != 2 {
		t.Fatalf("initial builds=%+v", first.Builds)
	}
	for _, build := range first.Builds {
		if build.Error != "" {
			t.Fatalf("enqueue=%+v", build)
		}
		projectCronReady(t, store.MemStore, build.DeploymentID)
	}
	if _, err := store.MemStore.ReconcileDeploymentCrons(t.Context()); err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(acct.Plan)
	for _, build := range first.Builds {
		if _, err := store.MemStore.CreateCronIfUnderQuota(t.Context(), build.AppID, "0 * * * *", "/", true, limits); err != nil {
			t.Fatal(err)
		}
	}
	// No source or app metadata changes: only removal of undeclared HTTP crons.
	ignored := apply(map[string]string{"no_triggers": "true"})
	if len(ignored.Builds) != 0 {
		t.Fatalf("no-triggers rebuilt: %+v", ignored.Builds)
	}
	changed := apply(nil)
	if len(changed.Builds) != 2 {
		t.Fatalf("cron-only builds=%+v", changed.Builds)
	}
	for _, build := range changed.Builds {
		rows, err := store.MemStore.ListCronsForApp(t.Context(), build.AppID)
		if err != nil || len(rows) != 1 || build.Configuration == nil || build.Configuration.Crons.Status != "pending" {
			t.Fatalf("early mutation/result: %+v %+v %v", build, rows, err)
		}
	}
	again := apply(nil)
	if len(again.Builds) != 2 {
		t.Fatalf("pending reuse=%+v", again.Builds)
	}
	for _, prior := range changed.Builds {
		found := false
		for _, build := range again.Builds {
			if build.AppID == prior.AppID {
				found = build.DeploymentID == prior.DeploymentID && build.BuildID == prior.BuildID
			}
		}
		if !found {
			t.Fatalf("duplicate build: %+v vs %+v", changed.Builds, again.Builds)
		}
	}
	for i, build := range changed.Builds {
		status := state.DeployLive
		if i == 1 {
			status = state.DeployFailed
		}
		if status == state.DeployLive {
			projectCronReady(t, store.MemStore, build.DeploymentID)
		} else if err := store.MemStore.UpdateDeploymentStatus(t.Context(), build.DeploymentID, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.MemStore.ReconcileDeploymentCrons(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i, build := range changed.Builds {
		rows, err := store.MemStore.ListCronsForApp(t.Context(), build.AppID)
		want := i
		if err != nil || len(rows) != want {
			t.Fatalf("partial activation app=%s rows=%+v want=%d err=%v", build.AppID, rows, want, err)
		}
		config, err := store.MemStore.DeploymentConfiguration(t.Context(), acct.ID, build.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		status := "activated"
		if i == 1 {
			status = "discarded"
		}
		if config.Crons.Status != status {
			t.Fatalf("configuration=%+v", config)
		}
	}
}

func TestProjectApplyCronSelectionAndPreview(t *testing.T) {
	for _, fields := range []map[string]string{{"only": "back"}, {"exclude": "worker"}, {"environment": "staging"}} {
		t.Run(fields["only"]+fields["exclude"]+fields["environment"], func(t *testing.T) {
			s, store, acct, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
			rec := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("initial=%d %s", rec.Code, rec.Body.String())
			}
			var first api.ApplyResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
				t.Fatal(err)
			}
			limits, _ := api.LimitsFor(acct.Plan)
			for _, build := range first.Builds {
				projectCronReady(t, store.MemStore, build.DeploymentID)
			}
			if _, err := store.MemStore.ReconcileDeploymentCrons(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, build := range first.Builds {
				if _, err := store.MemStore.CreateCronIfUnderQuota(t.Context(), build.AppID, "0 * * * *", "/", true, limits); err != nil {
					t.Fatal(err)
				}
			}
			if fields["environment"] == "staging" {
				if _, err := store.MemStore.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: acct.ID, ProjectID: first.ProjectID, Slug: "staging"}); err != nil {
					t.Fatal(err)
				}
			}
			rec = projectSecretsRequest(t, s, key, "/v1/projects", "", fields)
			if rec.Code != http.StatusOK {
				t.Fatalf("selected=%d %s", rec.Code, rec.Body.String())
			}
			var next api.ApplyResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
				t.Fatal(err)
			}
			for _, build := range next.Builds {
				projectCronReady(t, store.MemStore, build.DeploymentID)
			}
			if _, err := store.MemStore.ReconcileDeploymentCrons(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, build := range first.Builds {
				rows, err := store.MemStore.ListCronsForApp(t.Context(), build.AppID)
				want := 1
				app, appErr := store.MemStore.AppByID(t.Context(), build.AppID)
				if appErr != nil {
					t.Fatal(appErr)
				}
				if fields["environment"] == "production" || fields["environment"] == "" && app.WorkloadName == "back" {
					want = 0
				}
				if err != nil || len(rows) != want {
					t.Fatalf("selection %+v app=%s rows=%+v want=%d err=%v", fields, app.WorkloadName, rows, want, err)
				}
			}
		})
	}
}

func projectCronReady(t *testing.T, store *state.MemStore, id string) {
	t.Helper()
	if err := store.MarkDeploymentLive(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	dep, err := store.DeploymentByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if state.IsServiceRollout(dep) {
		if _, err := store.FinalizeServiceRollout(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectApplyProductionCronScope(t *testing.T) {
	s, store, acct, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("initial=%d %s", rec.Code, rec.Body.String())
	}
	var first api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	apps, err := store.MemStore.AppsForProject(t.Context(), acct.ID, first.ProjectID)
	if err != nil || len(apps) == 0 {
		t.Fatalf("apps=%+v %v", apps, err)
	}
	app := apps[0]
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, app.RootDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, app.RootDir, "Dockerfile"), []byte("FROM alpine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, plans := s.planProjectDeploymentCrons(t.Context(), acct, []state.App{app}, nil, []state.App{app}, nil)
	result := s.applyBuildsForAddedChangedOrdered(t.Context(), httptest.NewRequest("POST", "/v1/projects", nil), acct, state.Project{ID: first.ProjectID}, root, []reposcan.Workload{{Name: app.WorkloadName}}, nil, nil, changed, "production", plans)
	if len(result) != 1 || result[0].Error != "" {
		t.Fatalf("enqueue=%+v", result)
	}
	dep, err := store.MemStore.DeploymentByID(t.Context(), result[0].DeploymentID)
	if err != nil || dep.Scope != state.DefaultEnvScope {
		t.Fatalf("production alias=%+v %v", dep, err)
	}
	if result[0].Configuration == nil || result[0].Configuration.Crons.Status != "pending" {
		t.Fatalf("configuration=%+v", result)
	}
}
