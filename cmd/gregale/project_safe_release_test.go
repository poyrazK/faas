package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSafeProjectDeployWaitAndResumeObserveServerRelease(t *testing.T) {
	for _, terminal := range []string{"activated", "failed", "conflicted", "succeeded", "rolled_back", "rollback_failed"} {
		t.Run(terminal, func(t *testing.T) {
			var posts, reads int
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "old-release", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour)}
			verified := terminal == "succeeded" || terminal == "rolled_back" || terminal == "rollback_failed"
			expectedMode := "atomic"
			if verified {
				expectedMode = "atomic-verify"
				safe.Verification = &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: "app", Slug: "demo", Method: "GET", Path: "/health"}}}
			}
			var attempt string
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/projects/scan":
					if r.Header.Get(api.ProjectSafeReleaseHeader) != expectedMode {
						t.Error("scan was not safe")
					}
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "safe-plan", SafeRelease: safe})
				case "/v1/projects/safe-deploy":
					posts++
					attempt = r.Header.Get(api.DeploymentAttemptHeader)
					if err := r.ParseMultipartForm(1024 * 1024); err != nil {
						t.Fatal(err)
					}
					if r.FormValue("no_triggers") != "true" {
						t.Error("safe deploy applied trigger changes")
					}
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: attempt, PlanResponse: api.PlanResponse{SafeRelease: safe}, Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dark-candidate", BuildID: "build"}}})
				default:
					if r.Method != "GET" || r.URL.Path != "/v1/project-submissions/"+attempt {
						t.Errorf("unsafe request %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					reads++
					state := *safe
					state.Status = terminal
					if terminal == "activated" || terminal == "succeeded" || terminal == "rolled_back" || terminal == "rollback_failed" {
						state.ReleaseID = "new-release"
					}
					_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "demo", Environment: "production", Status: "complete", SelectionComplete: true, Apply: api.ApplyResponse{ProjectID: "project", PlanResponse: api.PlanResponse{SafeRelease: &state}, Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dark-candidate", BuildID: "build"}}}})
				}
			})
			archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			want := 0
			if terminal != "activated" && terminal != "succeeded" {
				want = 1
			}
			args := []string{"--project", "--atomic", "--project-slug", "demo", "--tarball", archive, "--yes"}
			if verified {
				args = append(args, "--verify")
			}
			if code := cmdDeployTarball(args); code != want {
				t.Fatalf("exit=%d stderr=%s", code, osStderr)
			}
			var receipt projectDeployReceipt
			if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.SafeRelease == nil || receipt.SafeRelease.Status != terminal || !receipt.RecoverySaved {
				t.Fatalf("receipt=%+v", receipt)
			}
			osStdout.(*bytes.Buffer).Reset()
			if code := cmdDeployResume(t.Context(), []string{receipt.SessionID, "--timeout", "1"}); code != want {
				t.Fatalf("resume exit=%d", code)
			}
			if posts != 1 || reads < 2 {
				t.Fatalf("posts=%d reads=%d", posts, reads)
			}
		})
	}
}
func TestSafeProjectDeployOldServerFailsBeforeApply(t *testing.T) {
	var scans int
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/scan" {
			t.Errorf("unsupported server was allowed to apply: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		scans++
		_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "old"})
	})
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	if code := cmdDeployTarball([]string{"--atomic", "--project", "--project-slug", "demo", "--tarball", archive, "--yes"}); code != 1 || scans != 1 {
		t.Fatalf("exit=%d scans=%d", code, scans)
	}
}

func TestVerifiedProjectDeployRejectsServerWithoutVerificationBeforeApply(t *testing.T) {
	posts := 0
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/scan" {
			posts++
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "atomic-only", SafeRelease: &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "old", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour)}})
	})
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	if code := cmdDeployTarball([]string{"--project", "--atomic", "--verify", "--project-slug", "demo", "--tarball", archive, "--yes"}); code != 1 || posts != 0 {
		t.Fatalf("exit=%d unexpected applies=%d", code, posts)
	}
}
