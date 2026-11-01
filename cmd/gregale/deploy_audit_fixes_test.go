package main_test

// CLI audit 2026-10-31, deploy path: a wait deadline is not a network
// failure, a project wait timeout is not a build failure, doctor's
// env-required check knows platform and app variables, a local-files upload
// names credential-looking files, rollback takes its slug anywhere, and
// deploy check names flags that need --project.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

// auditDeployCLI points the CLI at handler with JSON output and returns the
// buffers that replace osStdout and osStderr.
func auditDeployCLI(t *testing.T, handler http.HandlerFunc) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(main.WithFixtureCronStaging(main.WithFixturePolicyStaging(handler)))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	main.WithResetJSONOutput(t, true)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = stdout, stderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })
	return stdout, stderr
}

// Finding 1: a --json deploy whose wait reached --timeout reported
// "Could not reach Gregale (transport_error) … Check your network
// connection" and a `release wait --timeout 1200` next step.
func TestDeployJSONWaitTimeoutIsWaitStoppedNotTransport(t *testing.T) {
	stdout, _ := auditDeployCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			main.WriteJSONTest(w, api.AppResponse{ID: "a1", Slug: "my-app"})
		case "/v1/apps/my-app/deployments":
			main.WriteJSONTest(w, api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		case "/v1/deployments/d1":
			main.WriteJSONTest(w, api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
		default:
			http.NotFound(w, r)
		}
	})
	stderr, restore := main.CaptureStderr(t)
	defer restore()
	code := main.CmdDeployTarball([]string{"--image", "registry.x/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--name", "my-app", "--timeout", "1"})
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	var receipt main.DeployReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || !receipt.TimedOut {
		t.Fatalf("stdout receipt timed_out=%v err=%v\n%s", receipt.TimedOut, err, stdout.String())
	}
	var problem struct {
		api.Problem
		Recovery struct {
			NextCommand string `json:"next_command"`
		} `json:"recovery"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal([]byte(stderr.String()), &problem); err != nil {
		t.Fatalf("stderr is not one Problem: %v\n%s", err, stderr.String())
	}
	if problem.Code != "deployment_wait_stopped" || strings.Contains(problem.Title+problem.Hint, "network") || !strings.Contains(problem.Detail, "timed out after 1s") {
		t.Fatalf("wait deadline reported as %+v", problem.Problem)
	}
	if !strings.Contains(problem.Recovery.NextCommand, "--timeout 1") || strings.Contains(problem.Recovery.NextCommand, "--timeout 1200") {
		t.Fatalf("next_command = %q, want the run's --timeout 1", problem.Recovery.NextCommand)
	}
}

// Finding 2: a project wait timeout printed "project apply failed … builds
// failed=1" with deployment_status "timeout" and an error on the row.
func TestProjectDeployWaitTimeoutIsNotBuildFailure(t *testing.T) {
	for _, asJSON := range []bool{true, false} {
		t.Run(map[bool]string{true: "json", false: "text"}[asJSON], func(t *testing.T) {
			t.Chdir(t.TempDir())
			stdout, stderr := auditDeployCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/projects/scan":
					main.WriteJSONTest(w, api.PlanResponse{CanApply: true, PlanToken: "plan", Workloads: []api.PlanWorkload{{Name: "demo"}}})
				case "/v1/projects":
					main.WriteJSONTest(w, api.ApplyResponse{Apps: []api.ApplyResponseApp{{Slug: "demo", ID: "app"}}, Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dep", BuildID: "build"}}})
				case "/v1/deployments/dep":
					main.WriteJSONTest(w, api.DeploymentResponse{ID: "dep", AppID: "app", Status: "building"})
				default:
					http.NotFound(w, r)
				}
			})
			*main.JsonOutput = asJSON
			archive := main.WriteDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			code := main.CmdDeployTarball([]string{"--name", "timeout-project", "--tarball", archive, "--project", "--yes", "--timeout", "1"})
			if code != 3 {
				t.Fatalf("exit = %d, want 3\nstdout=%s\nstderr=%s", code, stdout, stderr)
			}
			if asJSON {
				var receipt struct {
					api.ApplyResponse
					TimedOut     bool `json:"timed_out"`
					WaitTimedOut []struct {
						Slug, Phase string
					} `json:"wait_timed_out"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
					t.Fatal(err)
				}
				row := receipt.Builds[0]
				if !receipt.TimedOut || len(receipt.WaitTimedOut) != 1 || receipt.WaitTimedOut[0].Slug != "demo" || receipt.WaitTimedOut[0].Phase != "deployment" || row.Error != "" || row.DeploymentStatus != "building" {
					t.Fatalf("receipt = %s", stdout)
				}
				if !strings.Contains(stderr.String(), "project apply wait timed out: 1 workload(s) still deploying") || strings.Contains(stderr.String(), "builds failed") {
					t.Fatalf("stderr = %s", stderr)
				}
				return
			}
			out := stdout.String()
			if !strings.Contains(out, "builds failed=0") || !strings.Contains(out, "wait timed out=1") || strings.Contains(out, "applied with failures") || !strings.Contains(out, "demo: deployment wait timed out") {
				t.Fatalf("stdout = %s", out)
			}
		})
	}
}

// doctorEnvCheck runs `gregale doctor --json` with args and returns the exit
// code and env-required check.
func doctorEnvCheck(t *testing.T, args ...string) (int, main.DoctorCheck) {
	t.Helper()
	main.WithResetJSONOutput(t, true)
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	defer func() { *main.OsStdout = old }()
	code := main.CmdDoctorWithImageInspector(args, nil)
	var report main.DoctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("doctor output: %v\n%s", err, stdout.String())
	}
	for _, check := range report.Checks {
		if check.Name == "env-required" {
			return code, check
		}
	}
	t.Fatal("env-required check missing")
	return code, main.DoctorCheck{}
}

// appEnvServer answers the app's secret and env name lists.
func appEnvServer(t *testing.T, app string, secrets, env []string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/" + app + "/secrets":
			var out api.AppSecretListResponse
			for _, key := range secrets {
				out.Secrets = append(out.Secrets, api.AppSecretResponse{Key: key, Scope: "default"})
			}
			main.WriteJSONTest(w, out)
		case "/v1/apps/" + app + "/env":
			var out api.AppEnvListResponse
			for _, key := range env {
				out.Env = append(out.Env, api.AppEnvResponse{Key: key})
			}
			main.WriteJSONTest(w, out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_fixture")
}

// Finding 3: env-required failed on GREGALE_* variables the platform
// injects and on variables set with `env set`, and blocked deploy check,
// doctor and start although it only read local files.
func TestDoctorEnvRequiredKnowsPlatformAndAppVariables(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")

	platform := t.TempDir()
	main.WriteFile(t, platform, "package.json", "{}")
	main.WriteFile(t, platform, "server.js", "const ca = process.env.GREGALE_SERVICE_CA_BUNDLE; const id = process.env.FAAS_APP_ID;\n")
	if code, check := doctorEnvCheck(t, platform); code != 0 || check.Status != "ok" {
		t.Fatalf("platform variables: exit=%d check=%+v", code, check)
	}
	if required, _, err := main.StartRequiredConfiguration(platform); err != nil || len(required) != 0 {
		t.Fatalf("start requires platform variables %v (err %v)", required, err)
	}

	export := filepath.Join(t.TempDir(), "customer-operation-export")
	if err := templates.Materialize("customer-operation-export", export); err != nil {
		t.Fatal(err)
	}
	if code, check := doctorEnvCheck(t, export); code != 0 || check.Status != "ok" {
		t.Fatalf("customer-operation-export: exit=%d check=%+v", code, check)
	}

	// The journey fixture reads variables the realtime journey sets with
	// `env set`; only the app knows them.
	probe := filepath.Join("..", "..", "tests", "journeys", "testdata", "probe-node")
	code, check := doctorEnvCheck(t, probe, "--app", "probe")
	if code != 0 || check.Status != "warn" || !strings.Contains(check.Hint, "not logged in") || !strings.Contains(strings.Join(check.Sources, ","), "E2E_REALTIME_CALLBACK_TOKEN") {
		t.Fatalf("logged out: exit=%d check=%+v", code, check)
	}
	if code, _ := doctorEnvCheck(t, probe, "--app", "probe", "--strict"); code != 1 {
		t.Fatalf("logged out --strict exit = %d, want 1 on the warning", code)
	}
	appEnvServer(t, "probe", []string{"E2E_REALTIME_CALLBACK_TOKEN"}, []string{"E2E_REALTIME_JWKS_B64"})
	if code, check := doctorEnvCheck(t, probe, "--app", "probe"); code != 0 || check.Status != "ok" {
		t.Fatalf("app declares the variables: exit=%d check=%+v", code, check)
	}
	appEnvServer(t, "probe", []string{"E2E_REALTIME_CALLBACK_TOKEN"}, nil)
	if code, check := doctorEnvCheck(t, probe, "--app", "probe"); code != 1 || check.Status != "error" || strings.Join(check.Sources, ",") != "E2E_REALTIME_JWKS_B64" {
		t.Fatalf("app lacks one variable: exit=%d check=%+v", code, check)
	}
}

// Finding 4: a local-files deploy uploads .env and key files without a
// word. It still uploads them (user decision) but names them.
func TestLocalFilesDeployWarnsAboutCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	main.WriteFile(t, dir, "package.json", "{}")
	main.WriteFile(t, dir, ".env", "LOCAL_ONLY=1\n")
	main.WriteFile(t, dir, ".env.example", "LOCAL_ONLY=\n")
	if err := os.MkdirAll(filepath.Join(dir, "certs"), 0o700); err != nil {
		t.Fatal(err)
	}
	main.WriteFile(t, filepath.Join(dir, "certs"), "server.key", "not a real key\n")
	var archive []byte
	stdout, stderr := auditDeployCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/account":
			main.WriteJSONTest(w, api.AccountResponse{Plan: "pro"})
		case r.URL.Path == "/v1/apps":
			main.WriteJSONTest(w, api.AppResponse{ID: "app-1", Slug: "probe-app"})
		case r.URL.Path == "/v1/apps/probe-app/deployments":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				if part.FormName() == "source" {
					archive, _ = io.ReadAll(part)
				}
			}
			main.WriteJSONTest(w, api.DeploymentResponse{ID: "dep-1", Status: "pending", AppID: "app-1"})
		default:
			http.NotFound(w, r)
		}
	})
	if code := main.CmdDeployTarball([]string{"--name", "probe-app", "--no-wait", "--no-doctor", "--secret-scan", "off"}); code != 0 {
		t.Fatalf("deploy exit=%d stderr=%s", code, stderr)
	}
	warning := ".env, certs/server.key, which may hold credentials"
	if !strings.Contains(stderr.String(), warning) || !strings.Contains(stderr.String(), ".gregaleignore") || !strings.Contains(stderr.String(), "gregale env push") || strings.Contains(stderr.String(), ".env.example") {
		t.Fatalf("stderr = %s", stderr)
	}
	var receipt main.DeployReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || len(receipt.Warnings) != 1 || !strings.Contains(receipt.Warnings[0], warning) {
		t.Fatalf("receipt warnings = %v (err %v)\n%s", receipt.Warnings, err, stdout)
	}
	packed := false
	for name := range main.ReadCapturedDeployArchive(t, archive) {
		packed = packed || strings.HasSuffix(name, "/.env") || name == ".env"
	}
	if !packed {
		t.Fatal("the warning changed what is packed: .env is missing from the upload")
	}
}

func TestCredentialFileName(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, "api/.env.production": true, ".npmrc": true, "certs/tls.pem": true, "server.KEY": true,
		"keys/id_rsa": true, "id_ed25519": true, ".aws/credentials": true,
		".env.example": false, "id_rsa.pub": false, "server.js": false, "keyboard.js": false, "credentials.md": false,
	} {
		if got := main.CredentialFileName(name); got != want {
			t.Errorf("credentialFileName(%q) = %v, want %v", name, got, want)
		}
	}
}

// Finding 5: `rollback --to v41 X` and `release rollback --app X` failed
// with "rollback takes one <slug>".
func TestRollbackTakesSlugAnywhereAndApp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	target := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/rollback" {
			t.Errorf("path = %q", r.URL.Path)
		}
		main.WriteJSONTest(w, api.DeploymentResponse{ID: target, Status: "live"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	for _, args := range [][]string{
		{"--to", target, "my-app"},
		{"--app", "my-app", "--to", target},
		{"--to=" + target, "--app=my-app"},
		{"my-app", "--app", "my-app"},
	} {
		if code := main.CmdRollback(args); code != 0 {
			t.Errorf("rollback %v = %d, want 0", args, code)
		}
	}
	if code := main.CmdRollback([]string{"other-app", "--app", "my-app"}); code != 1 {
		t.Errorf("conflicting slug and --app exit = %d, want 1", code)
	}
}

// Finding 7: `deploy check --worktree` answered "unknown flag".
func TestDeployCheckProjectOnlyFlagNamesProject(t *testing.T) {
	main.WithResetJSONOutput(t, false)
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	defer func() { *main.OsStderr = old }()
	for _, flag := range []string{"--worktree", "--env=staging", "--atomic"} {
		stderr.Reset()
		if code := main.CmdDeployCheck([]string{flag}); code != 1 || !strings.Contains(stderr.String(), "needs --project") {
			t.Errorf("deploy check %s exit=%d stderr=%q", flag, code, stderr.String())
		}
	}
}
