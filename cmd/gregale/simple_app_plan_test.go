package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

func TestResolveSimpleAppPlanUsesHostingOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("hosting:\n  port: 9000\n  health: /ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := main.ResolveSimpleAppPlan(dir, "demo", "small", simpleapp.SourceDirectory, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Framework != "node" || plan.Port != 9000 || plan.HealthPath != "/ready" || plan.ResourceProfile != "small" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestResolveSimpleAppPlanUsesCanonicalFrameworkDefaults(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		framework string
		port      int
		health    string
	}{
		{
			name: "express",
			files: map[string]string{
				"package.json": `{"dependencies":{"express":"^5"},"scripts":{"start":"node server.js"}}`,
				"server.js":    `app.get('/readyz', handler)`,
			},
			framework: "express",
			port:      3000,
			health:    "/readyz",
		},
		{
			name: "fastapi",
			files: map[string]string{
				"requirements.txt": "fastapi\nuvicorn\n",
				"app.py":           "from fastapi import FastAPI\napp = FastAPI()\n",
			},
			framework: "fastapi",
			port:      8000,
			health:    "/healthz",
		},
		{
			name: "go",
			files: map[string]string{
				"go.mod":  "module example.com/demo\n\ngo 1.24\n",
				"main.go": "package main\n",
			},
			framework: "go-net-http",
			port:      8080,
			health:    "/healthz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := main.ResolveSimpleAppPlan(dir, "demo", "", simpleapp.SourceDirectory, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Framework != tt.framework || plan.Port != tt.port || plan.HealthPath != tt.health {
				t.Fatalf("plan = %+v, want framework=%q port=%d health=%q", plan, tt.framework, tt.port, tt.health)
			}
		})
	}
}

func TestDeployPlanUsesCommittedProfileUnlessWorktreeSelected(t *testing.T) {
	repo := main.InitZeroConfigRepo(t)
	packageJSON := `{"dependencies":{"express":"^5"},"scripts":{"start":"node server.js"}}`
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "server.js"), []byte("app.get('/healthz', handler)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "package.json", "server.js"}, {"commit", "-q", "-m", "add express app"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "gregale.yaml"), []byte("hosting:\n  port: 9999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	main.WithCwd(t, repo)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))

	runPlan := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
		(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
		defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()
		if code := main.CmdDeployTarball(args); code != 0 {
			t.Fatalf("cmdDeployTarball(%v) exit = %d, stderr=%q", args, code, stderr.String())
		}
		return stdout.String()
	}

	committedPlan := runPlan("--plan", "--name", "demo")
	if !strings.Contains(committedPlan, "listener:           :3000 /healthz") || strings.Contains(committedPlan, ":9999") {
		t.Fatalf("committed plan did not use HEAD profile:\n%s", committedPlan)
	}

	worktreePlan := runPlan("--plan", "--worktree", "--name", "demo")
	if !strings.Contains(worktreePlan, "listener:           :9999 /healthz") {
		t.Fatalf("worktree plan did not use dirty hosting override:\n%s", worktreePlan)
	}
}

func TestResolveSimpleAppPlanRejectsFunctionShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte("exports.handler = () => {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ResolveSimpleAppPlan(dir, "demo", "", simpleapp.SourceDirectory, false, false); err == nil || !strings.Contains(err.Error(), "gregale deploy --diff") {
		t.Fatalf("error = %v, want function-path guidance", err)
	}
}

func TestRenderSimpleAppPlanExplainsEphemeralState(t *testing.T) {
	plan, err := simpleapp.Resolve(simpleapp.Spec{Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := main.RenderSimpleAppPlan(&out, plan, false); code != 0 {
		t.Fatalf("render code = %d", code)
	}
	for _, want := range []string{"scale to zero", "ephemeral", "durable state", "No remote state changed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q: %s", want, out.String())
		}
	}
}

func TestRenderSimpleAppPlanShowsTCPListenerReadinessForImage(t *testing.T) {
	plan, err := simpleapp.Resolve(simpleapp.Spec{Slug: "demo", Source: simpleapp.SourceImage})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := main.RenderSimpleAppPlan(&out, plan, false); code != 0 {
		t.Fatalf("renderSimpleAppPlan exit = %d", code)
	}
	if !strings.Contains(out.String(), "listener:           :8080 · TCP listener readiness") {
		t.Fatalf("plan did not explain TCP listener readiness:\n%s", out.String())
	}
}

func TestApplySimpleAppPlanToCreateRequestUsesResolvedDefaults(t *testing.T) {
	plan, err := simpleapp.Resolve(simpleapp.Spec{
		Slug:       "demo",
		Profile:    "small",
		HealthPath: "/ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := api.CreateAppRequest{Slug: "demo"}
	main.ApplySimpleAppPlanToCreateRequest(&req, plan)
	if req.Type != "app" || req.ExecutionMode != "request" || req.ResourceProfile != "small" || req.HealthPath != "/ready" {
		t.Fatalf("request = %+v", req)
	}
}

func TestApplyDeployLifecyclePreservesPlanDefaultsWhenFlagsOmitted(t *testing.T) {
	req := api.CreateAppRequest{
		ExecutionMode:    "request",
		RestartPolicy:    "on-failure",
		StartupDeadlineS: 30,
		MaxRetries:       2,
	}
	main.ApplyDeployLifecycleToCreateRequest(&req, "", "", 0, 0)
	if req.ExecutionMode != "request" || req.RestartPolicy != "on-failure" || req.StartupDeadlineS != 30 || req.MaxRetries != 2 {
		t.Fatalf("omitted flags changed plan defaults: %+v", req)
	}
	main.ApplyDeployLifecycleToCreateRequest(&req, "service", "always", 45, 5)
	if req.ExecutionMode != "service" || req.RestartPolicy != "always" || req.StartupDeadlineS != 45 || req.MaxRetries != 5 {
		t.Fatalf("explicit flags not applied: %+v", req)
	}
}

// deploy --plan must reject a manifest the deploy itself rejects (H10-67).
func TestDeployPlanRejectsInvalidManifest(t *testing.T) {
	repo := main.InitZeroConfigRepo(t)
	for name, body := range map[string]string{
		"package.json": `{"scripts":{"start":"node server.js"}}`,
		"server.js":    "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT || 3000)\n",
		"gregale.yaml": "scaling:\n  max_instances: -3\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "app with an invalid manifest"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	main.WithCwd(t, repo)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))

	var stdout, stderr bytes.Buffer
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()
	code := main.CmdDeployTarball([]string{"--plan", "--name", "demo"})
	if code == 0 || strings.Contains(stdout.String(), "Simple app plan") {
		t.Fatalf("plan accepted an invalid manifest: exit=%d stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "max_instances must be >= 0") {
		t.Fatalf("stderr does not explain the manifest error: %q", stderr.String())
	}
}
