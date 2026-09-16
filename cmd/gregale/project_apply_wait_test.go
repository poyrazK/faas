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
