package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
		switch r.URL.Path {
		case "/v1/apps/pr-42-web":
			writeJSONTest(w, api.AppResponse{ID: "preview-web", Slug: "pr-42-web", PreviewOfSlug: "web", PreviewPRNumber: 42, PreviewPRState: "open", PreviewExpiresAt: &expires, Status: "active", URL: "https://pr-42-web.gregale.dev"})
		case "/v1/apps/pr-42-web/deployments/latest":
			writeJSONTest(w, api.DeploymentResponse{ID: "deploy-42", AppID: "preview-web", Status: "failed", CreatedAt: "2026-09-17T08:00:00Z"})
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

	if code := cmdPreviewShow([]string{"pr-42-web"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got previewSummary
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.Kind != "pull_request" || got.URL == "" || got.LatestDeployment == nil || got.LatestDeployment.Status != "failed" {
		t.Fatalf("summary = %+v, want PR URL and failed deployment", got)
	}
}

func TestPreviewShowRejectsProductionAppBeforeDeploymentLookup(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/web" {
			writeJSONTest(w, api.AppResponse{Slug: "web", Status: "active"})
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

func setPreviewTestAuth(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
}
