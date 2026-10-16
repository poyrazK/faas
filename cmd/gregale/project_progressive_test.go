package main

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectProgressiveDeployContract(t *testing.T) {
	for _, outcome := range []string{"succeeded", "missing", "changed", "stabilized", "stabilization_missing"} {
		t.Run(outcome, func(t *testing.T) {
			raw := []byte(`{"env":{"demo":{"MODE":"canary"}}}`)
			_, hash, err := api.NormalizeProjectReleaseConfig(raw)
			if err != nil {
				t.Fatal(err)
			}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "baseline", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Configuration: &api.ProjectReleaseConfiguration{PreviousVersion: 1, PreviousHash: strings.Repeat("a", 64), TargetHash: strings.Repeat("b", 64), EnvironmentHash: strings.Repeat("c", 64), RequestHash: hash}, Verification: &api.ProjectReleaseVerification{Progressive: true, Probes: []api.ProjectReleaseProbe{{AppID: "app", Slug: "demo", Method: "GET", Path: "/health"}}}}
			stabilize := outcome == "stabilized" || outcome == "stabilization_missing"
			if stabilize {
				safe.Verification.Stabilization = &api.ProjectReleaseStabilization{Enabled: true, Status: "waiting"}
			}

			posts := 0
			attempt := ""
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/projects/scan":
					if r.Header.Get(api.ProjectProgressiveHeader) != "required" {
						t.Error("scan omitted progressive policy")
					}
					if stabilize && r.Header.Get(api.ProjectStabilizationHeader) != "required" {
						t.Error("scan omitted stabilization requirement")
					}
					planned := savedSafeProjectRelease(safe)
					if outcome == "stabilization_missing" {
						planned.Verification.Stabilization = nil
					}
					if outcome == "missing" {
						planned.Verification.Progressive = false
					}
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "progressive-plan", SafeRelease: planned})
				case "/v1/projects/safe-deploy":
					if r.Header.Get(api.ProjectProgressiveHeader) != "required" {
						t.Error("apply omitted progressive policy")
					}
					if stabilize && r.Header.Get(api.ProjectStabilizationHeader) != "required" {
						t.Error("apply omitted stabilization requirement")
					}
					posts++
					attempt = r.Header.Get(api.DeploymentAttemptHeader)
					staged := savedSafeProjectRelease(safe)
					staged.Status = "verifying"
					staged.ReleaseID = "candidate-graph"
					staged.Verification.Percent, staged.Verification.Generation = 5, 1
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: attempt, PlanResponse: api.PlanResponse{SafeRelease: staged}, Builds: []api.AppliedBuild{{AppID: "app", Slug: "demo", DeploymentID: "candidate", BuildID: "accepted"}}})
				default:
					terminal := savedSafeProjectRelease(safe)
					terminal.Status = "succeeded"
					terminal.ReleaseID = "candidate-graph"
					terminal.Verification.Percent, terminal.Verification.Generation = 100, 7
					if outcome == "changed" {
						terminal.Verification.Progressive = false
					}
					_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "demo", Environment: "production", Status: "complete", SelectionComplete: true, Apply: api.ApplyResponse{ProjectID: "project", PlanResponse: api.PlanResponse{SafeRelease: terminal}, Builds: []api.AppliedBuild{{AppID: "app", Slug: "demo", DeploymentID: "candidate", BuildID: "accepted"}}}})
				}
			})
			file := filepath.Join(t.TempDir(), "release.json")
			if err = os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			source := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			args := []string{"--project", "--atomic", "--verify", "--progressive", "--config-file", file, "--project-slug", "demo", "--tarball", source, "--timeout", "2", "--yes"}
			if stabilize {
				args = append(args, "--stabilize")
			}
			code := cmdDeployTarball(args)
			wanted := 1
			if outcome == "succeeded" || outcome == "stabilized" {
				wanted = 0
			}
			if code != wanted {
				t.Fatalf("exit=%d wanted=%d stderr=%s", code, wanted, osStderr)
			}
			if (outcome == "missing" || outcome == "stabilization_missing") && posts != 0 {
				t.Fatal("older server received progressive apply")
			}
		})
	}
}
