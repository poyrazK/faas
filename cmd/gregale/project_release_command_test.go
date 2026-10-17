package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectMigrationDeployRecovery(t *testing.T) {
	for _, outcome := range []string{"succeeded", "uncertain", "changed", "missing_ack"} {
		t.Run(outcome, func(t *testing.T) {
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "baseline", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: "app", Slug: "demo", Method: "GET", Path: "/health"}}}, ReleaseCommand: &api.ProjectReleaseCommand{AppID: "app", Slug: "demo", CommandHash: api.ProjectReleaseCommandHash([]string{"migrate"}, true), Compatibility: "backward-compatible", Status: "waiting", VerifyBaseline: true}}
			posts, reads := 0, 0
			var attempt string
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/projects/scan":
					if r.Header.Get(api.ProjectSafeReleaseHeader) != "atomic-migrate-verify" {
						t.Error("scan lost migration mode")
					}
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "migration-plan", SafeRelease: safe})
				case "/v1/projects/safe-deploy":
					if r.Header.Get(api.ProjectSafeReleaseHeader) != "atomic-migrate-verify" {
						t.Error("apply lost migration mode")
					}
					posts++
					attempt = r.Header.Get(api.DeploymentAttemptHeader)
					ack := attempt
					if outcome == "missing_ack" {
						ack = ""
					}
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: ack, PlanResponse: api.PlanResponse{SafeRelease: safe}, Builds: []api.AppliedBuild{{AppID: "app", Slug: "demo", DeploymentID: "candidate", BuildID: "build"}}})
				default:
					if r.Method != "GET" || r.URL.Path != "/v1/project-submissions/"+attempt {
						t.Errorf("unexpected recovery %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					reads++
					terminal := savedSafeProjectRelease(safe)
					terminal.Status = "succeeded"
					terminal.ReleaseID = "release"
					terminal.ReleaseCommand.Status = "succeeded"
					terminal.ReleaseCommand.TaskID = "migration-task"
					if outcome == "uncertain" {
						terminal.Status = "failed"
						terminal.ReleaseCommand.Status = "uncertain"
						terminal.ReleaseCommand.FailureCode = "lease_expired"
					}
					if outcome == "changed" {
						terminal.ReleaseCommand.CommandHash = strings.Repeat("f", 64)
					}
					_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "demo", Environment: "production", Status: "complete", SelectionComplete: true, Apply: api.ApplyResponse{ProjectID: "project", PlanResponse: api.PlanResponse{SafeRelease: terminal}, Builds: []api.AppliedBuild{{AppID: "app", Slug: "demo", DeploymentID: "candidate", BuildID: "build"}}}})
				}
			})
			source := writeDeploySourceArchive(t, map[string]string{"package.json": "{}", "Procfile": "release: migrate\n"})
			expected := 0
			if outcome != "succeeded" {
				expected = 1
			}
			if code := cmdDeployTarball([]string{"--project", "--atomic", "--verify", "--migrate", "--project-slug", "demo", "--tarball", source, "--timeout", "2", "--yes"}); code != expected {
				t.Fatalf("exit=%d expected=%d stderr=%s", code, expected, osStderr)
			}
			if posts != 1 {
				t.Fatalf("posts=%d", posts)
			}
			client, _ := authedClient()
			dir, err := deploySessionDir(client)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			var sessionID string
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					sessionID = strings.TrimSuffix(entry.Name(), ".json")
					data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(data, []byte("release: migrate")) {
						t.Fatal("journal stored migration source")
					}
				}
			}
			if sessionID == "" {
				t.Fatal("missing deploy journal")
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			osStdout.(*bytes.Buffer).Reset()
			resumeExpected := expected
			if outcome == "missing_ack" {
				resumeExpected = 0
			}
			if code := cmdDeployResume(t.Context(), []string{sessionID, "--timeout", "1"}); code != resumeExpected {
				t.Fatalf("resume=%d expected=%d", code, resumeExpected)
			}
			minimumReads := 2
			if outcome == "missing_ack" {
				minimumReads = 1
			}
			if posts != 1 || reads < minimumReads {
				t.Fatalf("resume submitted another migration: %d %d", posts, reads)
			}
		})
	}
}

func TestProjectMigrationOldServerFailsBeforeApply(t *testing.T) {
	posts := 0
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/scan" {
			posts++
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "old", SafeRelease: &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "old", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Verification: &api.ProjectReleaseVerification{}}})
	})
	source := writeDeploySourceArchive(t, map[string]string{"package.json": "{}", "Procfile": "release: migrate\n"})
	if code := cmdDeployTarball([]string{"--project", "--atomic", "--verify", "--migrate", "--project-slug", "demo", "--tarball", source, "--timeout", "2", "--yes"}); code != 1 || posts != 0 {
		t.Fatalf("exit=%d posts=%d", code, posts)
	}
}

func TestMigrationBaselineJournalOwnsEvidence(t *testing.T) {
	proof := &api.ProjectMigrationBaselineVerification{TaskID: "task", Status: "healthy", Evidence: []api.ProjectReleaseProbeEvidence{{StableRequests: api.RouteHealthProbeRequestsPerMinute}}}
	safe := &api.ProjectSafeRelease{ReleaseCommand: &api.ProjectReleaseCommand{VerifyBaseline: true, BaselineVerification: proof}}
	saved := savedSafeProjectRelease(safe)
	proof.Status = "unknown"
	proof.Evidence[0].StableRequests = 0
	if !saved.ReleaseCommand.VerifyBaseline || saved.ReleaseCommand.BaselineVerification.Status != "healthy" || saved.ReleaseCommand.BaselineVerification.Evidence[0].StableRequests != api.RouteHealthProbeRequestsPerMinute {
		t.Fatal("journal shares mutable baseline checkpoint")
	}
}

func TestMigrationDeployRefusesServerWithoutBaselineGate(t *testing.T) {
	posts := 0
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/scan" {
			posts++
			http.Error(w, "unexpected apply", http.StatusBadRequest)
			return
		}
		safe := &api.ProjectSafeRelease{Status: "building", Verification: &api.ProjectReleaseVerification{}, ReleaseCommand: &api.ProjectReleaseCommand{Compatibility: "backward-compatible", CommandHash: api.ProjectReleaseCommandHash([]string{"expand"}, false)}}
		_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "legacy-plan", SafeRelease: safe})
	})
	source := writeDeploySourceArchive(t, map[string]string{"package.json": "{}", "Procfile": "release: expand\n"})
	if code := cmdDeployTarball([]string{"--project", "--atomic", "--verify", "--migrate", "--project-slug", "demo", "--tarball", source, "--yes"}); code != 1 || posts != 0 {
		t.Fatalf("legacy server accepted: exit=%d writes=%d", code, posts)
	}
}
