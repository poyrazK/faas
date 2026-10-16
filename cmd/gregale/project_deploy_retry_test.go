package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectRetrySelectionPreservesHealthyAndPending(t *testing.T) {
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Fatalf("selection submitted work: %s", r.Method)
		}
		id := r.URL.Path[len("/v1/deployments/"):]
		status := map[string]string{"bad": "failed", "good": "live", "pending": "building"}[id]
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: "app-" + id, Status: status})
	})
	c, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	s := &savedDeploySession{record: deploySessionRecord{Project: &savedProjectDeploy{ID: "project", Workloads: []savedProjectWorkload{
		{Slug: "demo-bad", AppID: "app-bad", DeploymentID: "bad"},
		{Slug: "demo-good", AppID: "app-good", DeploymentID: "good"},
		{Slug: "demo-pending", AppID: "app-pending", DeploymentID: "pending"},
	}}}}
	project := api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: "project"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "demo-bad", WorkloadName: "back"}, {Slug: "demo-good", WorkloadName: "worker"}}}
	names, targets, err := selectProjectRetryTargets(t.Context(), c, s, project)
	if err != nil || len(names) != 1 || names[0] != "back" || len(targets) != 1 || targets["app-bad"] != "bad" {
		t.Fatalf("selection=%v %v %v", names, targets, err)
	}
	project.ID = "replaced"
	if _, _, err := selectProjectRetryTargets(t.Context(), c, s, project); err == nil {
		t.Fatal("replacement accepted")
	}
}

func TestProjectRetryAcceptanceFailsClosed(t *testing.T) {
	targets := map[string]string{"app": "old"}
	for _, tc := range []struct {
		name   string
		builds []api.AppliedBuild
		valid  bool
	}{
		{"new", []api.AppliedBuild{{AppID: "app", DeploymentID: "new"}}, true},
		{"rejected", []api.AppliedBuild{{AppID: "app", Error: "enqueue failed"}}, true},
		{"ignored", nil, false},
		{"reused", []api.AppliedBuild{{AppID: "app", DeploymentID: "old"}}, false},
		{"other", []api.AppliedBuild{{AppID: "other", DeploymentID: "new"}}, false},
		{"duplicate", []api.AppliedBuild{{AppID: "app", DeploymentID: "new"}, {AppID: "app", DeploymentID: "new2"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProjectRetryAcceptance(targets, api.ApplyResponse{Builds: tc.builds})
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestProjectRetryCommandSavesFreshAttemptAndForcesOnlyFailed(t *testing.T) {
	for _, mode := range []string{"tarball", "head", "worktree", "auto", "alias", "cwd"} {
		t.Run(mode, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var sourceDir string
			secret := "sk" + "_live_" + strings.Repeat("X", 28)
			if mode != "tarball" {
				sourceDir = initZeroConfigRepo(t)
				for name, body := range map[string]string{".gregaleignore": "ignored.txt\n", "ignored.txt": "ignored\n", "node_modules/hidden.txt": "hidden\n", ".env": "STRIPE_SECRET_KEY=" + secret + "\n"} {
					writeFile(t, sourceDir, name, body)
				}
				mustGit(t, sourceDir, "add", ".")
				mustGit(t, sourceDir, "commit", "-q", "-m", "source fixtures")
				writeFile(t, sourceDir, "package.json", `{ "name": "dirty" }`)
				writeFile(t, sourceDir, "local.txt", "untracked\n")
				if mode == "cwd" {
					t.Chdir(sourceDir)
				}
			}
			var archives []map[string][]byte
			captureSource := func(r *http.Request) {
				if err := r.ParseMultipartForm(1024 * 1024); err != nil {
					t.Fatal(err)
				}
				f, _, err := r.FormFile("source")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = f.Close() }()
				data, err := io.ReadAll(f)
				if err != nil {
					t.Fatal(err)
				}
				archives = append(archives, readCapturedDeployArchive(t, data))
			}

			var attempt string
			var applies int
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/account":
					_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "account", Plan: string(api.PlanPro)})
				case "/v1/projects/demo":
					_ = json.NewEncoder(w).Encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: "project"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "demo-back", WorkloadName: "back"}, {Slug: "demo-worker", WorkloadName: "worker"}}})
				case "/v1/deployments/failed":
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "failed", AppID: "back-app", Status: "failed"})
				case "/v1/deployments/healthy":
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "healthy", AppID: "worker-app", Status: "live"})
				case "/v1/projects/scan":
					captureSource(r)
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", Workloads: []api.PlanWorkload{{Name: "back"}}})
				case "/v1/projects":
					applies++
					captureSource(r)
					attempt = r.Header.Get(api.DeploymentAttemptHeader)
					if !api.ValidDeploymentAttemptID(attempt) || r.Header.Get(api.ProjectRetryHeader) != `{"back-app":"failed"}` {
						t.Fatalf("headers=%v", r.Header)
					}
					if err := r.ParseMultipartForm(1024 * 1024); err != nil {
						t.Fatal(err)
					}
					if r.FormValue("only") != "back" || r.FormValue("no_triggers") != "true" {
						t.Fatalf("selection=%v", r.MultipartForm.Value)
					}
					c, _ := authedClient()
					dir, _ := deploySessionDir(c)
					entries, _ := os.ReadDir(dir)
					if len(entries) < 2 {
						t.Error("retry was not saved before apply")
					}
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: attempt, Builds: []api.AppliedBuild{{Slug: "demo-back", AppID: "back-app", DeploymentID: "new-deploy", BuildID: "new-build"}}})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			c, err := authedClient()
			if err != nil {
				t.Fatal(err)
			}
			saved, lock, err := newSavedProjectDeploy(c, "demo", "", api.ApplyResponse{ProjectID: "project", Builds: []api.AppliedBuild{{Slug: "demo-back", AppID: "back-app", DeploymentID: "failed"}, {Slug: "demo-worker", AppID: "worker-app", DeploymentID: "healthy"}}})
			if err != nil {
				t.Fatal(err)
			}
			_ = lock.Close()
			args := []string{"retry", "--yes", "--no-wait", saved.record.ID}
			switch mode {
			case "tarball":
				args = append(args, "--tarball", writeDeploySourceArchive(t, map[string]string{"package.json": "{}"}))
			case "alias":
				args = append(args, "--worktree", "--path", sourceDir)
			case "cwd":
			default:
				args = append(args, "--path", sourceDir, "--source", mode)
			}
			if code := cmdDeployTarball(args); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, osStderr)
			}
			var receipt projectDeployReceipt
			if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if applies != 1 || receipt.SessionID == saved.record.ID || !receipt.RecoverySaved || receipt.SubmissionAttemptID != attempt {
				t.Fatalf("receipt=%+v applies=%d", receipt, applies)
			}

			if len(archives) != 2 {
				t.Fatalf("archives=%d", len(archives))
			}
			if mode != "tarball" {
				for _, entries := range archives {
					var packageBody []byte
					var local, config bool
					for name, body := range entries {
						if filepath.Base(name) == "package.json" {
							packageBody = body
						}
						if filepath.Base(name) == "local.txt" {
							local = true
						}
						if filepath.Base(name) == ".env" {
							config = true
						}
						if strings.Contains(name, "node_modules/") || filepath.Base(name) == "ignored.txt" || strings.Contains(name, "/.git/") {
							t.Errorf("excluded source uploaded: %s", name)
						}
						if bytes.Contains(body, []byte(secret)) {
							t.Errorf("raw credential uploaded in %s", name)
						}
					}
					dirty := mode == "worktree" || mode == "alias"
					if bytes.Contains(packageBody, []byte("dirty")) != dirty || local != dirty || !config {
						t.Fatalf("wrong source policy: mode=%s package=%s local=%v config=%v", mode, packageBody, local, config)
					}
				}
			}
		})
	}
}

func TestProjectRetryRejectsConflictingSourceFlagsBeforeRequests(t *testing.T) {
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid source made request: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	for _, flags := range [][]string{
		{"--tarball", "source.tar.gz", "--path", "."},
		{"--tarball", "source.tar.gz", "--source", "auto"},
		{"--tarball", "source.tar.gz", "--worktree"},
		{"--source", "head", "--worktree"},
		{"--source", "unknown"},
	} {
		if code := cmdDeployRetry(t.Context(), append([]string{strings.Repeat("a", 32)}, flags...)); code != 1 {
			t.Fatalf("flags=%v exit=%d", flags, code)
		}
	}
}
