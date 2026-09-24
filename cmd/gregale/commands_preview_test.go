package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPreviewListUsesLinkedAppAndIncludesLatestDeployment(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	root := t.TempDir()
	if _, err := saveProjectContext(root, localProjectContext{Version: projectContextVersion, Project: "shop", App: "web"}); err != nil {
		t.Fatalf("save project context: %v", err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, ".gregale")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	expires := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/apps":
			writeJSONTest(w, []api.AppResponse{
				{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", PreviewExpiresAt: &expires, Status: "active", URL: "https://pr-42-web.gregale.dev"},
				{ID: "preview-other", Slug: "pr-7-other", PreviewOfSlug: "other", PreviewPRNumber: 7, PreviewPRState: "open", Status: "active"},
				{ID: "production", Slug: "web", Status: "active"},
			})
		case "/v1/deployments/latest-by-app":
			writeJSONTest(w, api.LatestDeploymentsByAppResponse{Items: []api.DeploymentResponse{{ID: "deploy-42", AppID: "preview-web", Status: "succeeded", CreatedAt: "2026-09-17T08:00:00Z"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true

	if code := cmdPreviewList(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got []previewSummary
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].ParentSlug != "web" || got[0].PRNumber != 42 {
		t.Fatalf("items = %+v, want only web PR #42", got)
	}
	if got[0].LatestDeployment == nil || got[0].LatestDeployment.Status != "succeeded" {
		t.Fatalf("latest deployment = %+v, want succeeded", got[0].LatestDeployment)
	}
	if !reflect.DeepEqual(paths, []string{"/v1/apps", "/v1/deployments/latest-by-app"}) {
		t.Errorf("paths = %v", paths)
	}
}

func TestPreviewShowIncludesLatestDeployment(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	expires := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/preview/pr-42-web" {
			http.NotFound(w, r)
			return
		}
		writeJSONTest(w, api.PreviewResourceResponse{
			App:                  api.AppResponse{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", PreviewExpiresAt: &expires, Status: "active", URL: "https://pr-42-web.gregale.dev"},
			LatestDeployment:     &api.DeploymentResponse{ID: "deploy-42", AppID: "preview-web", Status: "failed", CreatedAt: "2026-09-17T08:00:00Z"},
			ProductionDeployment: &api.DeploymentResponse{ID: "deploy-prod", Status: "live"},
			Changes:              api.PreviewProductionChangesResponse{ArtifactChanged: true, ConfigurationChangedGroups: []string{"runtime"}},
			Links:                api.PreviewResourceLinksResponse{Logs: "/v1/apps/pr-42-web/logs", Metrics: "/v1/apps/pr-42-web/metrics", Configuration: "/v1/apps/pr-42-web"},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true

	if code := cmdPreviewShow([]string{"pr-42-web"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got previewSummary
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.Kind != "pull_request" || got.URL == "" || got.LatestDeployment == nil || got.LatestDeployment.Status != "failed" ||
		got.ProductionDeployment == nil || got.Changes == nil || !got.Changes.ArtifactChanged || got.Links == nil {
		t.Fatalf("summary = %+v, want first-class preview details", got)
	}
}

func TestPreviewShowUsesCurrentHeadEnvironment(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/preview/pr-42-web":
			writeJSONTest(w, api.PreviewResourceResponse{
				App:              api.AppResponse{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", Status: "active"},
				LatestDeployment: &api.DeploymentResponse{ID: "old-root", AppID: "preview-web", Status: statusLive},
			})
		case "/v1/preview/pr-42-web/environment":
			writeJSONTest(w, previewEnvironmentFixture("building", false, "building"))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdPreviewShow([]string{"pr-42-web"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got previewSummary
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.Environment == nil || got.Environment.Phase != "building" || got.Environment.TotalWorkloads != 2 {
		t.Fatalf("environment = %+v", got.Environment)
	}
	root := got.Environment.Members[0]
	if root.Links == nil || root.Links.URL != "https://pr-42-web.gregale.dev" || root.Changes == nil ||
		!root.Changes.ArtifactChanged || root.ExpiresAt == nil {
		t.Fatalf("root member diagnostics = %+v", root)
	}
	if got.LatestDeployment == nil || got.LatestDeployment.ID != "current-root" {
		t.Fatalf("latest deployment = %+v, want current-head root", got.LatestDeployment)
	}
}

func TestRenderPreviewEnvironmentDetailsIncludesPerWorkloadDiffAndLinks(t *testing.T) {
	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	environment := previewEnvironmentFixture("live", true, statusLive)
	renderPreviewEnvironmentDetails(environment)
	for _, fragment := range []string{
		"Artifact: differs from production", "Config:   routing", "https://pr-42-web.gregale.dev",
		"/v1/apps/pr-42-web/logs", "/v1/apps/pr-42-worker/metrics",
	} {
		if !strings.Contains(out.String(), fragment) {
			t.Errorf("preview details missing %q: %s", fragment, out.String())
		}
	}
}

func TestRenderPreviewEnvironmentDetailsDoesNotCompareUnavailableArtifact(t *testing.T) {
	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	environment := previewEnvironmentFixture("live", true, statusLive)
	environment.Members[0].Changes.PreviewArtifact.DeploymentID = ""
	renderPreviewEnvironmentDetails(environment)
	if strings.Contains(out.String(), "Artifact: matches production") ||
		!strings.Contains(out.String(), "Artifact: current-head artifact unavailable") {
		t.Fatalf("unavailable preview artifact was misclassified: %s", out.String())
	}
}

func TestPreviewShowRejectsProductionAppBeforeDeploymentLookup(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/preview/web" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected request: %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	if code := cmdPreviewShow([]string{"web"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestPreviewEnvironmentLookupOnlyFallsBackOnNotFound(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "forbidden", "Forbidden", "deployment read permission required"))
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "test-token")
	if got, err := previewEnvironmentForApp(context.Background(), client, api.AppResponse{Slug: "dev-web"}); err != nil || got != nil || requests.Load() != 0 {
		t.Fatalf("developer preview lookup = %+v, %v; requests = %d", got, err, requests.Load())
	}
	_, err := previewEnvironmentForApp(context.Background(), client, api.AppResponse{Slug: "pr-42-web", PreviewPRNumber: 42})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Status != http.StatusForbidden {
		t.Fatalf("error = %v, want forbidden API error", err)
	}
}

func TestPreviewWaitReturnsReadyReceipt(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	oldInterval := previewWaitPollInterval
	previewWaitPollInterval = time.Millisecond
	defer func() { previewWaitPollInterval = oldInterval }()
	expires := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	var latestReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/pr-42-web":
			writeJSONTest(w, api.AppResponse{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", PreviewExpiresAt: &expires, Status: "active", URL: "https://pr-42-web.gregale.dev", CanonicalURL: "https://preview.example.com"})
		case "/v1/apps/pr-42-web/deployments/latest":
			status := "pending"
			if latestReads.Add(1) > 1 {
				status = statusLive
			}
			writeJSONTest(w, api.DeploymentResponse{ID: "deploy-42", AppID: "preview-web", Status: status, CreatedAt: "2026-09-17T08:00:00Z"})
		case "/v1/deployments/deploy-42":
			writeJSONTest(w, api.DeploymentResponse{ID: "deploy-42", AppID: "preview-web", Status: statusLive, CreatedAt: "2026-09-17T08:00:00Z"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	defer func() { osStdout = oldOut }()
	jsonOutput = true

	if code := cmdPreviewWait([]string{"pr-42-web", "--timeout", "1"}); code != 0 {
		t.Fatalf("cmdPreviewWait exit = %d, want 0", code)
	}
	var got previewWaitReceipt
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if !got.Ready || got.Preview.URL != "https://preview.example.com" {
		t.Fatalf("receipt = %+v, want ready preview URL", got)
	}
	if got.Deployment == nil || got.Deployment.ID != "deploy-42" || got.Deployment.Status != statusLive {
		t.Fatalf("deployment = %+v, want live deploy-42", got.Deployment)
	}
}

func TestPreviewWaitRequiresWholeEnvironment(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	oldInterval := previewWaitPollInterval
	previewWaitPollInterval = time.Millisecond
	defer func() { previewWaitPollInterval = oldInterval }()
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/pr-42-web":
			writeJSONTest(w, api.AppResponse{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", Status: "active", URL: "https://pr-42-web.gregale.dev"})
		case "/v1/apps/pr-42-web/deployments/latest":
			writeJSONTest(w, api.DeploymentResponse{ID: "old-root", AppID: "preview-web", Status: statusLive})
		case "/v1/preview/pr-42-web/environment":
			if reads.Add(1) == 1 {
				oldHead := previewEnvironmentFixture("building", false, "building")
				oldHead.CommitSHA = "old-sha"
				oldHead.Members[0].DeploymentID = "old-root"
				writeJSONTest(w, oldHead)
			} else {
				writeJSONTest(w, previewEnvironmentFixture("live", true, statusLive))
			}
		case "/v1/deployments/current-root":
			writeJSONTest(w, api.DeploymentResponse{ID: "current-root", AppID: "preview-web", Status: statusLive})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true

	if code := cmdPreviewWait([]string{"pr-42-web", "--timeout", "1"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got previewWaitReceipt
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if reads.Load() < 2 || !got.Ready || got.Environment == nil || got.Environment.LiveWorkloads != 2 || got.Environment.CommitSHA != "current-sha" {
		t.Fatalf("receipt = %+v, environment reads = %d", got, reads.Load())
	}
	if got.Deployment == nil || got.Deployment.ID != "current-root" {
		t.Fatalf("deployment = %+v, want current-head root", got.Deployment)
	}
}

func TestPreviewWaitFailsForSiblingDeployment(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/pr-42-web":
			writeJSONTest(w, api.AppResponse{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", Status: "active"})
		case "/v1/apps/pr-42-web/deployments/latest":
			writeJSONTest(w, api.DeploymentResponse{ID: "current-root", AppID: "preview-web", Status: statusLive})
		case "/v1/preview/pr-42-web/environment":
			writeJSONTest(w, previewEnvironmentFixture("failed", false, "failed"))
		case "/v1/deployments/current-root":
			writeJSONTest(w, api.DeploymentResponse{ID: "current-root", AppID: "preview-web", Status: statusLive})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true

	if code := cmdPreviewWait([]string{"pr-42-web", "--timeout", "1"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var got previewWaitReceipt
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.Ready || got.Environment == nil || got.Environment.Phase != "failed" || got.NextAction != "gregale logs pr-42-worker --deployment worker-deploy --follow" {
		t.Fatalf("receipt = %+v", got)
	}
}

func previewEnvironmentFixture(phase string, ready bool, workerStatus string) api.PreviewEnvironmentStatusResponse {
	live := 1
	if ready {
		live = 2
	}
	return api.PreviewEnvironmentStatusResponse{
		RootSlug: "pr-42-web", PRNumber: 42, CommitSHA: "current-sha", Phase: phase,
		Ready: ready, Summary: "PR preview " + phase, LiveWorkloads: live, TotalWorkloads: 2,
		Members: []api.PreviewEnvironmentMemberResponse{
			{AppID: "preview-web", Slug: "pr-42-web", WorkloadName: "web", AppStatus: "active", PreviewState: "open", DeploymentID: "current-root", DeploymentStatus: statusLive,
				ExpiresAt: previewTimePtr(time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)),
				Changes: &api.PreviewProductionChangesResponse{ArtifactChanged: true,
					PreviewArtifact:            api.PreviewArtifactResponse{DeploymentID: "current-root", CommitSHA: "current-sha"},
					ProductionArtifact:         api.PreviewArtifactResponse{DeploymentID: "production-root", CommitSHA: "production-sha"},
					ConfigurationChangedGroups: []string{"routing"}},
				Links: &api.PreviewResourceLinksResponse{URL: "https://pr-42-web.gregale.dev", Logs: "/v1/apps/pr-42-web/logs", Metrics: "/v1/apps/pr-42-web/metrics", Configuration: "/v1/apps/pr-42-web"}},
			{AppID: "preview-worker", Slug: "pr-42-worker", WorkloadName: "worker", AppStatus: "active", PreviewState: "open", DeploymentID: "worker-deploy", DeploymentStatus: workerStatus,
				Changes: &api.PreviewProductionChangesResponse{ConfigurationChangedGroups: []string{}},
				Links:   &api.PreviewResourceLinksResponse{URL: "https://pr-42-worker.gregale.dev", Logs: "/v1/apps/pr-42-worker/logs", Metrics: "/v1/apps/pr-42-worker/metrics", Configuration: "/v1/apps/pr-42-worker"}},
		},
	}
}

func previewTimePtr(value time.Time) *time.Time { return &value }

func TestPreviewWaitTimeoutKeepsResumeReceipt(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	oldInterval := previewWaitPollInterval
	previewWaitPollInterval = time.Millisecond
	defer func() { previewWaitPollInterval = oldInterval }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/pr-42-web":
			writeJSONTest(w, api.AppResponse{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", Status: "active", URL: "https://pr-42-web.gregale.dev"})
		case "/v1/apps/pr-42-web/deployments/latest":
			writeJSONTest(w, api.DeploymentResponse{ID: "deploy-42", AppID: "preview-web", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &stdout, &stderr
	defer func() { osStdout, osStderr = oldOut, oldErr }()
	jsonOutput = true

	if code := cmdPreviewWait([]string{"pr-42-web", "--timeout", "1"}); code != 3 {
		t.Fatalf("cmdPreviewWait timeout exit = %d, want 3", code)
	}
	var got previewWaitReceipt
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode timeout output: %v\n%s", err, stdout.String())
	}
	if !got.TimedOut || got.Ready || got.ResumeCommand != "gregale preview wait pr-42-web --timeout 1" {
		t.Fatalf("timeout receipt = %+v", got)
	}
	if got.NextAction != "gregale logs pr-42-web --deployment deploy-42 --follow" {
		t.Fatalf("next action = %q", got.NextAction)
	}
	if !strings.Contains(stderr.String(), "wait deadline") {
		t.Fatalf("stderr = %q, want wait deadline hint", stderr.String())
	}
}

func setPreviewTestAuth(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
}
