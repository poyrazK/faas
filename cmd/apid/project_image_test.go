// adr: 678, 680, 682
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectComposeImageScanAndApply(t *testing.T) {
	t.Run("ordering", func(t *testing.T) { testProjectComposeImageScanAndApply(t, false) })
	t.Run("healthy_dependency", func(t *testing.T) { testProjectComposeImageScanAndApply(t, true) })
}

func testProjectComposeImageScanAndApply(t *testing.T, healthy bool) {
	t.Helper()
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	compose := `services:
  gateway:
    image: nginx:1.27
    command: [nginx, -g, "daemon off;"]
    ports: ["8080:80"]
    healthcheck: {test: [CMD, /check], timeout: 250ms}
    depends_on: [worker]
  worker:
    image: ghcr.io/example/worker:v1
    command: ["/app/worker", "with spaces"]
`
	if healthy {
		compose = strings.Replace(compose, "depends_on: [worker]", "depends_on:\n      worker:\n        condition: service_healthy", 1)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "compose.yaml", Mode: 0o644, Size: int64(len(compose))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(compose)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	post := func(route string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("source", "source.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(archive.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("project_slug", "image-project"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, route, &body)
		req.Header.Set("Authorization", "Bearer "+e.key)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", route, rec.Code, rec.Body)
		}
		return rec
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(post("/v1/projects/scan").Body.Bytes(), &plan); err != nil || !plan.CanApply || len(plan.Workloads) != 2 || plan.Workloads[0].Image == "" {
		t.Fatalf("scan = %+v, %v", plan, err)
	}
	if check := plan.Workloads[0].ImageHealthcheck; check == nil || check.Test[1] != "/check" || check.TimeoutNS != 250000000 {
		t.Fatalf("scan plan lost Compose healthcheck: %+v", check)
	}
	if healthy && plan.Workloads[0].DependsOnConditions["worker"] != api.ComposeDependencyHealthy {
		t.Fatalf("scan lost dependency condition: %+v", plan.Workloads[0])
	}
	var applied api.ApplyResponse
	if err := json.Unmarshal(post("/v1/projects").Body.Bytes(), &applied); err != nil || len(applied.Builds) != 2 {
		t.Fatalf("apply = %+v, %v", applied, err)
	}
	if applied.Builds[0].Slug != "worker" || applied.Builds[1].Slug != "gateway" {
		t.Fatalf("deployment order = %+v", applied.Builds)
	}
	for _, result := range applied.Builds {
		if result.Error != "" || result.DeploymentID == "" || result.BuildID != "" {
			t.Fatalf("image result = %+v", result)
		}
		dep, err := e.store.DeploymentByID(t.Context(), result.DeploymentID)
		if err != nil || dep.Kind != state.DeploymentKindImage || dep.SourcePath != "" || !dep.FullRootfsAllowAuto {
			t.Fatalf("image deployment = %+v, %v", dep, err)
		}
		captured, err := frameworkprofile.ImageCommandFromProfile(dep.InferredProfile)
		wantCommand := map[string]string{"gateway": "nginx,-g,daemon off;", "worker": "/app/worker,with spaces"}[result.Slug]
		if err != nil || captured == nil || strings.Join(captured.Cmd, ",") != wantCommand {
			t.Fatalf("project apply did not capture Compose CMD: %s, %v", dep.InferredProfile, err)
		}
		if healthy && result.Slug == "gateway" {
			reader := e.store
			gate, err := reader.CheckDeploymentDependencies(t.Context(), dep.ID, time.Now())
			var blocker *state.DependencyGateError
			if !errors.As(err, &blocker) || !blocker.Pending() || gate.Dependencies[0].DeploymentID != applied.Builds[0].DeploymentID {
				t.Fatalf("project apply lost its exact dependency: %+v, %v", gate, err)
			}
		}
		if result.Slug == "gateway" {
			check, err := frameworkprofile.ImageHealthcheckFromProfile(dep.InferredProfile)
			if err != nil || check == nil || check.Override == nil || check.Override.Test[1] != "/check" || check.Override.TimeoutNS != 250000000 {
				t.Fatalf("apply lost Compose healthcheck: %s, %v", dep.InferredProfile, err)
			}
		}
		if result.Slug == "gateway" && dep.OverridePort != 80 {
			t.Fatalf("used host port instead of container port: %+v", dep)
		}
		if _, err := e.store.BuildByDeployment(t.Context(), dep.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("image service queued a source build: %v", err)
		}
	}
	app, err := e.store.AppBySlug(t.Context(), "gateway")
	if err != nil || len(app.Manifest.ServiceBindings) != 1 || app.Manifest.ProjectSourceSHA256 == "" {
		t.Fatalf("image configuration/checkpoint = %+v, %v", app, err)
	}
	worker, err := e.store.AppBySlug(t.Context(), "worker")
	if err != nil || worker.Manifest.ExecutionMode != api.ExecutionModeWorker {
		t.Fatalf("background image lifecycle = %+v, %v", worker.Manifest, err)
	}
	for _, event := range notifier.emitted {
		if event.Channel == db.NotifyBuildQueued {
			t.Fatal("image apply notified builderd")
		}
	}
	applied = api.ApplyResponse{}
	if err := json.Unmarshal(post("/v1/projects").Body.Bytes(), &applied); err != nil || len(applied.Builds) != 0 {
		t.Fatalf("unchanged reapply = %+v, %v", applied, err)
	}
}

func TestEnqueueBuild_ProjectImageUsesImageWorker(t *testing.T) {
	for _, kind := range []githubdpb.EnqueueBuildEventKind{
		githubdpb.EnqueueBuildEventKind_EVENT_KIND_PUSH, githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			store := &bridgeStubStore{app: state.App{ID: "app-1", AccountID: "acct-1", Status: state.AppActive,
				Manifest: state.AppManifest{ProjectImage: "ghcr.io/example/app:v1", ProjectImagePort: 3000, ProjectImageCommand: []string{"serve", "with spaces"}, ProjectImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"NONE"}}}}}
			notifier := &bridgeStubNotifier{}
			bridge := newBridge(t, store, notifier)
			path, size := stageFixtureFile(t, bridge.stagingRoot, "acct-1/app-1/abc123", []byte("fixture"))
			resp, err := bridge.EnqueueBuild(t.Context(), &githubdpb.EnqueueBuildRequest{
				AccountId: "acct-1", AppId: "app-1", CommitSha: "abc123", SourcePath: path,
				SourceUrl: "https://codeload.example.com/repo/tar.gz/abc123", SourceBytes: size,
				EventKind: kind, Pusher: "octocat", DeploymentScope: "staging"})
			if err != nil || resp == nil || resp.BuildId != "" || resp.DeploymentId == "" {
				t.Fatalf("image bridge = %+v, %v", resp, err)
			}
			dep := store.createDeploymentReturned
			if dep.Kind != state.DeploymentKindImage || dep.OverridePort != 3000 || dep.Scope != "staging" || dep.PusherLogin != "octocat" || !strings.HasSuffix(dep.ImageDigest, ":v1") {
				t.Fatalf("image intent = %+v", dep)
			}
			captured, err := frameworkprofile.ImageCommandFromProfile(dep.InferredProfile)
			if err != nil || captured == nil || strings.Join(captured.Cmd, ",") != "serve,with spaces" {
				t.Fatalf("GitHub image command was not captured: %s, %v", dep.InferredProfile, err)
			}
			check, err := frameworkprofile.ImageHealthcheckFromProfile(dep.InferredProfile)
			if err != nil || check == nil || check.Override == nil || check.Override.Test[0] != "NONE" {
				t.Fatalf("GitHub image healthcheck was not captured: %s, %v", dep.InferredProfile, err)
			}
			if len(notifier.channels) != 1 || notifier.channels[0] != db.NotifyDeploymentChanged || len(store.updateStatusCalls) != 0 {
				t.Fatalf("image queue = %v, statuses = %v", notifier.channels, store.updateStatusCalls)
			}
		})
	}
}
