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

func TestPairedProjectDeployWireJournalAndResume(t *testing.T) {
	raw := []byte(`{"env":{"demo":{"MODE":"proposed-value-journal-sentinel"}}}`)
	canonical, requestHash, err := api.NormalizeProjectReleaseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var attempt string
	posts, reads := 0, 0
	safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "old-release", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: "app", Slug: "demo", Method: "GET", Path: "/health"}}}, Configuration: &api.ProjectReleaseConfiguration{PreviousVersion: 1, PreviousHash: strings.Repeat("a", 64), TargetHash: strings.Repeat("b", 64), EnvironmentHash: strings.Repeat("c", 64), RequestHash: requestHash}}
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/scan", "/v1/projects/safe-deploy":
			if r.Header.Get(api.ProjectSafeReleaseHeader) != "atomic-config-verify" {
				t.Error("paired mode was not preserved")
			}
			if err := r.ParseMultipartForm(1024 * 1024); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("release_config") != string(canonical) || r.FormValue("no_triggers") != "true" {
				t.Error("configuration body or trigger suppression changed")
			}
			if r.URL.Path == "/v1/projects/scan" {
				_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "paired-plan", SafeRelease: safe})
				return
			}
			posts++
			attempt = r.Header.Get(api.DeploymentAttemptHeader)
			_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: attempt, PlanResponse: api.PlanResponse{SafeRelease: safe}, Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "candidate", BuildID: "build"}}})
		default:
			if r.URL.Path != "/v1/project-submissions/"+attempt || r.Method != "GET" {
				t.Errorf("unexpected recovery request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			reads++
			terminal := savedSafeProjectRelease(safe)
			terminal.Status = "rolled_back"
			terminal.ReleaseID = "candidate-release"
			terminal.RestoredReleaseID = "restored-release"
			terminal.Configuration.TargetVersion = 2
			terminal.Configuration.RestoredVersion = 3
			_ = json.NewEncoder(w).Encode(api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "demo", Environment: "production", Status: "complete", SelectionComplete: true, Apply: api.ApplyResponse{ProjectID: "project", PlanResponse: api.PlanResponse{SafeRelease: terminal}, Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "candidate", BuildID: "build"}}}})
		}
	})
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	if code := cmdDeployTarball([]string{"--project", "--atomic", "--verify", "--config-file", path, "--project-slug", "demo", "--tarball", archive, "--yes"}); code != 1 {
		t.Fatalf("exit=%d stderr=%s", code, osStderr)
	}
	var receipt projectDeployReceipt
	if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SafeRelease == nil || receipt.SafeRelease.Configuration.RestoredVersion != 3 || !receipt.RecoverySaved {
		t.Fatalf("receipt=%+v", receipt)
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
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("proposed-value-journal-sentinel")) {
				t.Fatal("configuration values leaked into journal")
			}
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	osStdout.(*bytes.Buffer).Reset()
	if code := cmdDeployResume(t.Context(), []string{receipt.SessionID, "--timeout", "1"}); code != 1 {
		t.Fatalf("resume=%d", code)
	}
	if posts != 1 || reads < 2 {
		t.Fatalf("resume reapplied config: posts=%d reads=%d", posts, reads)
	}
}
func TestPairedProjectDeployOldServerOrWrongConfigFailsBeforeApply(t *testing.T) {
	for _, response := range []string{"missing", "wrong_hash"} {
		t.Run(response, func(t *testing.T) {
			posts := 0
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/projects/scan" {
					posts++
					http.NotFound(w, r)
					return
				}
				safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "old", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: "app", Method: "GET", Path: "/health"}}}}
				if response == "wrong_hash" {
					safe.Configuration = &api.ProjectReleaseConfiguration{RequestHash: strings.Repeat("f", 64)}
				}
				_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "old", SafeRelease: safe})
			})
			path := filepath.Join(t.TempDir(), "release.json")
			if err := os.WriteFile(path, []byte(`{"env":{"demo":{"MODE":"new"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			if code := cmdDeployTarball([]string{"--project", "--atomic", "--verify", "--config-file", path, "--project-slug", "demo", "--tarball", archive, "--yes"}); code != 1 || posts != 0 {
				t.Fatalf("exit=%d applies=%d", code, posts)
			}
		})
	}
}
