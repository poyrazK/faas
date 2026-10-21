package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func deployReliabilityCLI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(withFixtureCronStaging(withFixturePolicyStaging(handler)))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	withResetJSONOutput(t, true)
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &bytes.Buffer{}, &bytes.Buffer{}
	t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
}

func TestDeployReliabilitySecretsFileExcluded(t *testing.T) {
	for _, mode := range []string{"directory", "worktree", "head", "head-path", "head-workspace", "worktree-workspace", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if mode != "directory" {
				dir = initZeroConfigRepo(t)
			}
			t.Chdir(dir)
			sourceDir := dir
			args := []string{"--name", "probe-app", "--no-wait", "--no-doctor", "--secret-scan", "off"}
			if mode == "worktree" || mode == "symlink" || mode == "worktree-workspace" {
				args = append(args, "--worktree")
			}
			if mode == "head-path" || strings.HasSuffix(mode, "workspace") {
				sourceDir = filepath.Join(dir, "service")
				if err := os.MkdirAll(sourceDir, 0700); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--path", sourceDir)
			}
			if err := os.WriteFile(filepath.Join(sourceDir, "package.json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(mode, "workspace") {
				if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"private":true,"workspaces":["service"]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			secretPath := filepath.Join(sourceDir, "production.secrets")
			const content = "API_TOKEN=fixture-secret-value\n"
			if err := os.WriteFile(secretPath, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sourceDir, ".gregaleignore"), []byte("!production.secrets\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if mode != "directory" {
				if _, err := runGitCmd(dir, "add", "."); err != nil {
					t.Fatal(err)
				}
				if _, err := runGitCmd(dir, "commit", "-m", "fixture"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "symlink" {
				alias := filepath.Join(sourceDir, "secret-alias")
				if err := os.Symlink(secretPath, alias); err != nil {
					t.Fatal(err)
				}
				secretPath = alias
			}
			args = append(args, "--secrets-file", secretPath)
			var archive []byte
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v1/account":
					_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
				case r.URL.Path == "/v1/apps":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "probe-app"})
				case strings.Contains(r.URL.Path, "/secrets/"):
					w.WriteHeader(http.StatusNoContent)
				case r.URL.Path == "/v1/apps/probe-app/deployments":
					reader, err := r.MultipartReader()
					if err != nil {
						t.Error(err)
						return
					}
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Error(err)
							return
						}
						if part.FormName() == "source" {
							archive, _ = io.ReadAll(part)
						}
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-1", Status: "pending", AppID: "app-1"})
				default:
					http.NotFound(w, r)
				}
			})
			code := cmdDeployTarball(args)
			if mode == "symlink" {
				if code != 1 || len(archive) != 0 {
					t.Fatalf("symlink exit=%d archive bytes=%d", code, len(archive))
				}
				return
			}
			if code != 0 {
				t.Fatalf("deploy exit=%d", code)
			}
			entries := readCapturedDeployArchive(t, archive)
			if len(entries) == 0 {
				t.Fatal("empty source archive")
			}
			for name, data := range entries {
				if strings.Contains(name, "production.secrets") || strings.Contains(name, "secret-alias") || bytes.Contains(data, []byte(content)) {
					t.Fatalf("secrets file uploaded at %s", name)
				}
			}
		})
	}
}

func TestDeployReliabilityProjectSecretsTravelInApplyBeforeWaiting(t *testing.T) {
	t.Chdir(t.TempDir())
	secretPath := filepath.Join(t.TempDir(), "production.secrets")
	if err := os.WriteFile(secretPath, []byte("DATABASE_URL=fixture-database\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var events []string
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/scan":
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "fixture-plan", Workloads: []api.PlanWorkload{{Name: "api"}}})
		case "/v1/projects":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			found := false
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					return
				}
				body, _ := io.ReadAll(part)
				if part.FormName() == "initial_secrets" {
					var bundle api.ProjectInitialSecrets
					if err := json.Unmarshal(body, &bundle); err != nil {
						t.Error(err)
					}
					found = bundle["api"]["DATABASE_URL"] == "fixture-database"
				}
			}
			if !found {
				t.Error("project apply missing secret bundle")
			}
			events = append(events, "apply-with-secrets")
			_ = json.NewEncoder(w).Encode(api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "api", DeploymentID: "dep-api"}}})
		case "/v1/deployments/dep-api":
			events = append(events, "readiness-fails")
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-api", Status: "failed", Error: "missing required secret"})
		case "/v1/apps/api/secrets/DATABASE_URL":
			events = append(events, "set-secret")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	code := cmdDeployTarball([]string{"--name", "probe-project", "--tarball", archive, "--project", "--yes", "--secrets-file", secretPath})
	if code != 1 || strings.Join(events, ",") != "apply-with-secrets,readiness-fails" {
		t.Fatalf("exit=%d events=%v", code, events)
	}

}

func TestDeployReliabilityWaitEndPreservesAcceptedDeploymentTrigger(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "timeout"
		if interrupted {
			name = "interrupted"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			t.Chdir(t.TempDir())
			accepted, removed := false, false
			app := api.AppResponse{ID: "app-1", Slug: "probe-app"}
			policyUpdates := 0
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "probe-app"})
				case "/v1/apps/probe-app":
					if r.Method == http.MethodPatch {
						var req api.UpdateAppRequest
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						if req.Profiling != nil {
							app.Manifest.Profiling = req.Profiling
							policyUpdates++
						}
						if req.Tracing != nil {
							app.Manifest.Tracing = req.Tracing
							policyUpdates++
						}
					}
					_ = json.NewEncoder(w).Encode(app)
				case "/v1/account":
					_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
				case "/v1/crons":
					if r.Method == http.MethodGet {
						_ = json.NewEncoder(w).Encode([]api.CronResponse{})
						return
					}
					_ = json.NewEncoder(w).Encode(api.CronResponse{ID: "cron-1", AppID: "app-1", Schedule: "0 3 * * *", Path: "/run", Enabled: true})
				case "/v1/apps/probe-app/deployments":
					_, _ = io.Copy(io.Discard, r.Body)
					accepted = true
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-accepted", AppID: "app-1", Status: "building"})
				case "/v1/deployments/dep-accepted":
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-accepted", AppID: "app-1", Status: "building"})
				case "/v1/crons/cron-1":
					removed = r.Method == http.MethodDelete
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			})
			archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}", "Dockerfile": "FROM scratch\n", "gregale.yaml": "profiling:\n  enabled: true\ntracing:\n  enabled: true\ntriggers:\n  - kind: cron\n    app: probe-app\n    schedule: '0 3 * * *'\n    path: /run\n"})
			execution := deployExecution{}
			if interrupted {
				execution.onQueued = func(api.DeploymentResponse) { cancel() }
			}
			code := cmdDeployTarballToExisting(ctx, []string{"--name", "probe-app", "--tarball", archive, "--dockerfile", "--timeout", "1"}, false, execution)
			wantCode := 3
			if interrupted {
				wantCode = 130
			}
			if policyUpdates != 2 || app.Manifest.Profiling == nil || !app.Manifest.Profiling.Enabled || app.Manifest.Tracing == nil || !app.Manifest.Tracing.Enabled {
				t.Fatalf("accepted policy updates=%d profiling=%+v tracing=%+v", policyUpdates, app.Manifest.Profiling, app.Manifest.Tracing)
			}
			if code != wantCode || !accepted || removed {
				t.Fatalf("exit=%d accepted=%v triggerRemoved=%v", code, accepted, removed)
			}

		})
	}
}

func TestDeployReliabilityAutomaticBranchRetryKeyIsFresh(t *testing.T) {
	t.Chdir(t.TempDir())
	var keys []string
	deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/probe-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "probe-app"})
		case "/v1/apps/probe-app/deployments/source-ref":
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-1", AppID: "app-1", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	})
	args := []string{"--name", "probe-app", "--repo", "acme/demo", "--ref", "main", "--no-wait"}
	for i := 0; i < 2; i++ {
		if code := cmdDeployTarball(args); code != 0 {
			t.Fatalf("deploy exit=%d", code)
		}
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] == keys[1] {
		t.Fatalf("automatic retry keys=%v", keys)
	}

}
