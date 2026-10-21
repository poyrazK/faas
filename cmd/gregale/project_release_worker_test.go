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

func TestProjectWorkerDeployRecovery(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed", "rolled_back", "changed", "missing", "missing_health", "changed_health"} {
		t.Run(outcome, func(t *testing.T) {
			worker := &api.ProjectReleaseWorker{AppID: "worker", Slug: "consumer", BindingID: "binding", BindingHash: strings.Repeat("a", 64), PreviousDeploymentID: "previous-worker", DrainSeconds: 30, Status: "building"}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "baseline", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Worker: worker, Verification: &api.ProjectReleaseVerification{WorkerHealth: &api.ProjectWorkerHealth{Status: "insufficient_evidence"}, Probes: []api.ProjectReleaseProbe{{AppID: "web", Slug: "demo", Method: "GET", Path: "/health"}}}}
			posts, reads := 0, 0
			var attempt string
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/projects/scan":
					if r.Header.Get(api.ProjectWorkerHealthHeader) != "required" {
						t.Error("lost worker health at scan")
					}
					if r.Header.Get(api.ProjectSafeReleaseHeader) != "atomic-verify-workers" {
						t.Error("lost workers mode")
					}
					planned := savedSafeProjectRelease(safe)
					if outcome == "missing" {
						planned.Worker = nil
					}
					if outcome == "missing_health" {
						planned.Verification.WorkerHealth = nil
					}
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "worker-plan", SafeRelease: planned})
				case "/v1/projects/safe-deploy":
					if r.Header.Get(api.ProjectWorkerHealthHeader) != "required" {
						t.Error("lost worker health at apply")
					}
					if r.Header.Get(api.ProjectSafeReleaseHeader) != "atomic-verify-workers" {
						t.Error("lost workers mode at apply")
					}
					posts++
					attempt = r.Header.Get(api.DeploymentAttemptHeader)
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: attempt, PlanResponse: api.PlanResponse{SafeRelease: safe}, Builds: []api.AppliedBuild{{AppID: "web", Slug: "demo", DeploymentID: "candidate-web", BuildID: "web-build"}, {AppID: "worker", Slug: "consumer", DeploymentID: "candidate-worker", BuildID: "worker-build"}}})
				default:
					if r.Method != "GET" || r.URL.Path != "/v1/project-submissions/"+attempt {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					reads++
					terminal := savedSafeProjectRelease(safe)
					terminal.Status = outcome
					terminal.ReleaseID = "release"
					terminal.Worker.DeploymentID = "candidate-worker"
					terminal.Worker.InstanceID = "worker-instance"
					terminal.Worker.Status = "active"
					if outcome == "rolled_back" {
						terminal.RestoredReleaseID = "restored"
						terminal.Worker.Status = "restored"
					}
					if outcome == "changed_health" {
						terminal.Status = "succeeded"
						terminal.Verification.WorkerHealth = nil
					}
					if outcome == "changed" {
						terminal.Status = "succeeded"
						terminal.Worker.BindingHash = strings.Repeat("b", 64)
					}
					_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "demo", Environment: "production", Status: "complete", SelectionComplete: true, Apply: api.ApplyResponse{ProjectID: "project", PlanResponse: api.PlanResponse{SafeRelease: terminal}, Builds: []api.AppliedBuild{{AppID: "web", Slug: "demo", DeploymentID: "candidate-web", BuildID: "web-build"}, {AppID: "worker", Slug: "consumer", DeploymentID: "candidate-worker", BuildID: "worker-build"}}}})
				}
			})
			source := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			expected := 1
			if outcome == "succeeded" {
				expected = 0
			}
			if code := cmdDeployTarball([]string{"--project", "--atomic", "--verify", "--workers", "--worker-health", "--project-slug", "demo", "--tarball", source, "--timeout", "2", "--yes"}); code != expected {
				t.Fatalf("exit=%d expected=%d stderr=%s", code, expected, osStderr)
			}
			if outcome == "missing" || outcome == "missing_health" {
				if posts != 0 {
					t.Fatal("old server received worker apply")
				}
				return
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
			sessionID := ""
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					sessionID = strings.TrimSuffix(entry.Name(), ".json")
					data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(data, []byte(worker.BindingHash)) {
						t.Fatal("journal lost binding identity")
					}
				}
			}
			if sessionID == "" {
				t.Fatal("missing recovery session")
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			osStdout.(*bytes.Buffer).Reset()
			if code := cmdDeployResume(t.Context(), []string{sessionID, "--timeout", "1"}); code != expected {
				t.Fatalf("resume=%d", code)
			}
			if posts != 1 || reads < 2 {
				t.Fatalf("recovery resubmitted worker: %d %d", posts, reads)
			}
		})
	}
}
