// adr: 679, 680, 682
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedPublishedImageApp(t *testing.T, store state.Store, accountID, slug string) state.App {
	t.Helper()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: accountID, Slug: slug,
		Manifest: state.AppManifest{ProjectImage: "ghcr.io/team/api:production", ProjectImagePort: 3000,
			ProjectImageCommand: []string{"serve", "with spaces"}, ProjectImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"CMD", "/check"}}, Env: map[string]string{"CONFIG": "retained"}}})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func publishedTestImage(char string) string {
	return "ghcr.io/team/api@sha256:" + strings.Repeat(char, 64)
}

func TestImagePublishedDeliveryAndConfiguration(t *testing.T) {
	e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
	app := seedPublishedImageApp(t, e.store, e.acct.ID, "published-api")
	previous, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		Status: state.DeployLive, ImageDigest: publishedTestImage("b"), Scope: "default", ReleaseCommand: []string{"echo", "release"}})
	if err != nil {
		t.Fatal(err)
	}
	route := "/v1/apps/" + app.Slug + "/image-published"
	request := api.CreateDeploymentRequest{Image: publishedTestImage("a")}
	deliveryHeaders := map[string]string{"Idempotency-Key": "publication"}
	first := e.do(t, http.MethodPost, route, request, deliveryHeaders)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first delivery = %d: %s", first.Code, first.Body)
	}
	var response api.DeploymentResponse
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.DeploymentByID(t.Context(), response.ID)
	if err != nil || dep.Kind != state.DeploymentKindImage || dep.ImageDigest != request.Image || dep.BuildID != "" || dep.OverridePort != 3000 || !dep.FullRootfsAllowAuto || len(dep.ReleaseCommand) != 2 || dep.ReleaseCommand[1] != "release" {
		t.Fatalf("published deployment = %+v, %v", dep, err)
	}
	captured, err := frameworkprofile.ImageCommandFromProfile(dep.InferredProfile)
	if err != nil || captured == nil || strings.Join(captured.Cmd, ",") != "serve,with spaces" {
		t.Fatalf("publisher did not capture Compose CMD: %s, %v", dep.InferredProfile, err)
	}
	check, err := frameworkprofile.ImageHealthcheckFromProfile(dep.InferredProfile)
	if err != nil || check == nil || check.Override == nil || check.Override.Test[1] != "/check" {
		t.Fatalf("publisher did not capture Compose healthcheck: %s, %v", dep.InferredProfile, err)
	}
	if _, err := e.store.BuildByDeployment(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("published image queued a source build: %v", err)
	}
	// Duplicate delivery has a different HTTP idempotency key, but still
	// consumes neither another deployment row nor another notification.
	replayRequest := request
	replayRequest.Scope = api.DefaultEnvScope
	replayRequest.Overrides = &api.CreateDeploymentOverrides{Port: 4000}
	replayed := e.do(t, http.MethodPost, route, replayRequest, map[string]string{"Idempotency-Key": "another-delivery"})
	if replayed.Code != http.StatusOK {
		t.Fatalf("duplicate = %d: %s", replayed.Code, replayed.Body)
	}
	var duplicate api.DeploymentResponse
	if err := json.Unmarshal(replayed.Body.Bytes(), &duplicate); err != nil || duplicate.ID != dep.ID {
		t.Fatalf("duplicate = %+v, %v", duplicate, err)
	}
	unchanged, err := e.store.DeploymentByID(t.Context(), dep.ID)
	if err != nil || unchanged.OverridePort != 3000 {
		t.Fatalf("replay changed the accepted configuration: %+v, %v", unchanged, err)
	}
	imageNotifications := 0
	for _, event := range notifier.emitted {
		if event.Channel == db.NotifyBuildQueued {
			t.Fatal("published image notified builderd")
		}
		if event.Channel == db.NotifyDeploymentChanged {
			imageNotifications++
		}
	}
	if imageNotifications != 1 {
		t.Fatalf("image notifications = %d, want one", imageNotifications)
	}
	// Failure retries return the original outcome rather than creating work
	// that could undo a deliberate rollback to the still-serving predecessor.
	if _, err := e.store.SetDeploymentFailed(t.Context(), dep.ID, api.CodeImageNotFound, "registry failure"); err != nil {
		t.Fatal(err)
	}
	replayed = e.do(t, http.MethodPost, route, request, deliveryHeaders)
	if replayed.Code != http.StatusOK || json.Unmarshal(replayed.Body.Bytes(), &duplicate) != nil || duplicate.ID != dep.ID || duplicate.Status != string(state.DeployFailed) {
		t.Fatalf("terminal replay = %d: %s", replayed.Code, replayed.Body)
	}
	old, err := e.store.DeploymentByID(t.Context(), previous.ID)
	if err != nil || old.Status != state.DeployLive {
		t.Fatalf("publisher displaced serving traffic before verification: %+v, %v", old, err)
	}
	request.Scope = "staging"
	scoped := e.do(t, http.MethodPost, route, request, nil)
	if scoped.Code != http.StatusAccepted || json.Unmarshal(scoped.Body.Bytes(), &duplicate) != nil || duplicate.ID == dep.ID || duplicate.Scope != "staging" {
		t.Fatalf("independent scope = %d: %s", scoped.Code, scoped.Body)
	}
	scopedDep, err := e.store.DeploymentByID(t.Context(), duplicate.ID)
	if err != nil || len(scopedDep.ReleaseCommand) != 0 {
		t.Fatalf("copied default-scope release command to staging: %+v, %v", scopedDep, err)
	}
	after, err := e.store.AppByID(t.Context(), app.ID)
	if err != nil || after.Manifest.ProjectImage != app.Manifest.ProjectImage || after.Manifest.ProjectImageCommand[1] != "with spaces" || after.Manifest.Env["CONFIG"] != "retained" {
		t.Fatalf("publisher changed project intent: %+v, %v", after, err)
	}
}

func TestImagePublishedValidationAndAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		image    string
		scope    string
		declared string
		signed   bool
		want     int
	}{
		{"tag rejected", "ghcr.io/team/api:production", "", "ghcr.io/team/api:production", false, 400},
		{"other repository", "ghcr.io/team/other@sha256:" + strings.Repeat("a", 64), "", "ghcr.io/team/api:production", false, 400},
		{"other registry", "example.com/team/api@sha256:" + strings.Repeat("a", 64), "", "ghcr.io/team/api:production", false, 400},
		{"source workload", publishedTestImage("a"), "", "", false, 400},
		{"pinned declaration", publishedTestImage("a"), "", publishedTestImage("b"), false, 400},
		{"invalid scope", publishedTestImage("a"), "__all__", "ghcr.io/team/api:production", false, 400},
		{"signature policy", publishedTestImage("a"), "", "ghcr.io/team/api:production", true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
			app := seedPublishedImageApp(t, e.store, e.acct.ID, "published-invalid")
			manifest := app.Manifest
			manifest.ProjectImage = tc.declared
			if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest, RequireSigned: &tc.signed, SetRequireSigned: true}); err != nil {
				t.Fatal(err)
			}
			rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/image-published", api.CreateDeploymentRequest{Image: tc.image, Scope: tc.scope}, nil)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
			if err != nil || len(rows) != 0 {
				t.Fatalf("rejected publisher created deployments: %d, %v", len(rows), err)
			}
		})
	}
}

func TestImagePublishedWorkflowInheritance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		clear bool
	}{
		{"omitted workflows inherit", false},
		{"explicit empty workflows clear", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
			app := seedPublishedImageApp(t, e.store, e.acct.ID, "published-workflows")
			workflows, err := json.Marshal(workflowDeploymentRequest().Workflows)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				Status: state.DeployLive, ImageDigest: publishedTestImage("b"), Scope: api.DefaultEnvScope, Workflows: workflows}); err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"image": publishedTestImage("a")}
			if tc.clear {
				body["workflows"] = []api.WorkflowSpec{}
			}
			rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/image-published", body, nil)
			var response api.DeploymentResponse
			if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
				t.Fatalf("publication = %d: %s", rec.Code, rec.Body)
			}
			dep, err := e.store.DeploymentByID(t.Context(), response.ID)
			if err != nil {
				t.Fatal(err)
			}
			var actual []api.WorkflowSpec
			if len(dep.Workflows) > 0 {
				if err := json.Unmarshal(dep.Workflows, &actual); err != nil {
					t.Fatal(err)
				}
			}
			if tc.clear && len(actual) != 0 || !tc.clear && (len(actual) != 1 || actual[0].Name != "process_order") {
				t.Fatalf("published workflow definitions = %s", dep.Workflows)
			}
		})
	}
}

func TestImagePublishedCanonicalReference(t *testing.T) {
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	app := seedPublishedImageApp(t, e.store, e.acct.ID, "published-canonical")
	manifest := app.Manifest
	manifest.ProjectImage = "docker.io/library/nginx:production"
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	var firstID string
	for _, tc := range []struct {
		image string
		want  int
	}{
		{"docker.io/nginx@sha256:" + strings.Repeat("a", 64), http.StatusAccepted},
		{"docker.io/library/nginx@sha256:" + strings.Repeat("a", 64), http.StatusOK},
		{"docker.io/library/nginx@sha256:" + strings.Repeat("b", 64), http.StatusAccepted},
	} {
		rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/image-published", api.CreateDeploymentRequest{Image: tc.image}, nil)
		var response api.DeploymentResponse
		if rec.Code != tc.want || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
			t.Fatalf("publication of %s = %d: %s", tc.image, rec.Code, rec.Body)
		}
		if firstID == "" {
			firstID = response.ID
		} else if tc.want == http.StatusOK && response.ID != firstID || tc.want == http.StatusAccepted && response.ID == firstID {
			t.Fatalf("canonical/different digest delivery returned the wrong identity: %+v", response)
		}
		if !strings.HasPrefix(response.ImageDigest, "docker.io/library/nginx@sha256:") {
			t.Fatalf("publication did not persist a canonical reference: %+v", response)
		}
	}
}

func TestImagePublishedWorkerPlanGate(t *testing.T) {
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanFree)
	app := seedPublishedImageApp(t, e.store, e.acct.ID, "published-worker")
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeWorker
	if _, err := e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/image-published", api.CreateDeploymentRequest{Image: publishedTestImage("a")}, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("plan-denied publication created deployments: %d, %v", len(rows), err)
	}
}

func TestImagePublishedDeployTokenAndScope(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeAppsRead})
	app := seedPublishedImageApp(t, e.store, e.acct.ID, "published-bound")
	other := seedPublishedImageApp(t, e.store, e.acct.ID, "published-other")
	body := api.CreateDeploymentRequest{Image: publishedTestImage("a")}
	if rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/image-published", body, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only key accepted: %d %s", rec.Code, rec.Body)
	}
	plain, hash, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateDeployToken(t.Context(), e.acct.ID, app.ID, hash, "publisher", []string{api.ScopeDeployWrite}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.key = plain
	if rec := e.do(t, http.MethodPost, "/v1/apps/"+other.Slug+"/image-published", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("token crossed app boundary: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/image-published", body, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("bound publisher = %d %s", rec.Code, rec.Body)
	}
}
