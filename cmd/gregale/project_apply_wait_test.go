package main

// adr: 050

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWaitForProjectApply_ZeroBuilds(t *testing.T) {
	apply, timedOut := waitForProjectApply(t.Context(), nil, api.ApplyResponse{}, time.Second)
	if timedOut || len(apply.Builds) != 0 {
		t.Fatalf("zero-build wait = %+v, timedOut=%v", apply, timedOut)
	}
}

func TestWaitForProjectApply_MixedTerminalStates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/dep-live"):
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-live", Status: statusLive})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/dep-fail"):
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-fail", Status: deploymentStatusFailed, Error: "health check failed"})
		case strings.HasPrefix(r.URL.Path, "/v1/builds/build-live"):
			_ = json.NewEncoder(w).Encode(api.BuildResponse{ID: "build-live", Status: api.BuildStatusSucceeded})
		case strings.HasPrefix(r.URL.Path, "/v1/builds/build-fail"):
			_ = json.NewEncoder(w).Encode(api.BuildResponse{ID: "build-fail", Status: api.BuildStatusFailed})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	apply := api.ApplyResponse{Builds: []api.AppliedBuild{
		{Slug: "live", DeploymentID: "dep-live", BuildID: "build-live"},
		{Slug: "fail", DeploymentID: "dep-fail", BuildID: "build-fail"},
	}}
	got, timedOut := waitForProjectApply(t.Context(), client, apply, time.Second)
	if timedOut {
		t.Fatal("mixed terminal states reported timeout")
	}
	if got.Builds[0].DeploymentStatus != statusLive || got.Builds[0].BuildStatus != api.BuildStatusSucceeded || got.Builds[0].Error != "" {
		t.Fatalf("live result = %+v", got.Builds[0])
	}
	if got.Builds[1].DeploymentStatus != deploymentStatusFailed || got.Builds[1].BuildStatus != api.BuildStatusFailed || !strings.Contains(got.Builds[1].Error, "health check failed") {
		t.Fatalf("failed result = %+v", got.Builds[1])
	}
}

func TestWaitForProjectApply_DeadlineMarksEveryPendingBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-queued", Status: "queued"})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	apply := api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "queued", DeploymentID: "dep-queued", BuildID: "build-queued"}}}
	got, timedOut := waitForProjectApply(t.Context(), client, apply, 20*time.Millisecond)
	if !timedOut {
		t.Fatal("queued project wait did not report timeout")
	}
	if got.Builds[0].DeploymentStatus != "timeout" || got.Builds[0].BuildStatus != "timeout" || got.Builds[0].Error == "" {
		t.Fatalf("timeout result = %+v", got.Builds[0])
	}
}

// adr: 678
func TestWaitForProjectApply_ImageDeployment(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		wantStatus string
		wantError  bool
		timedOut   bool
	}{
		{"live", statusLive, statusLive, false, false},
		{"failed", deploymentStatusFailed, deploymentStatusFailed, true, false},
		{"pending", "queued", "timeout", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v1/builds/") {
					t.Error("image deployment requested a source build")
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "image-deployment", Status: tc.status})
			}))
			defer srv.Close()
			deadline := time.Second
			if tc.timedOut {
				deadline = 20 * time.Millisecond
			}
			apply := api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "image", DeploymentID: "image-deployment"}}}
			got, timedOut := waitForProjectApply(t.Context(), NewClient(srv.URL, "token"), apply, deadline)
			result := got.Builds[0]
			if timedOut != tc.timedOut || result.DeploymentStatus != tc.wantStatus || result.BuildStatus != "" || (result.Error != "") != tc.wantError {
				t.Fatalf("image wait = %+v, timedOut=%v", result, timedOut)
			}
		})
	}
}
