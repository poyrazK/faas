package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	plan, err := resolveSimpleAppPlan(dir, "demo", "small", simpleapp.SourceDirectory, false, false)
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
			plan, err := resolveSimpleAppPlan(dir, "demo", "", simpleapp.SourceDirectory, false, false)
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
	repo := initZeroConfigRepo(t)
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

	withCwd(t, repo)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	runPlan := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		oldOut, oldErr := osStdout, osStderr
		osStdout, osStderr = &stdout, &stderr
		defer func() { osStdout, osStderr = oldOut, oldErr }()
		if code := cmdDeployTarball(args); code != 0 {
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
	if _, err := resolveSimpleAppPlan(dir, "demo", "", simpleapp.SourceDirectory, false, false); err == nil || !strings.Contains(err.Error(), "function path") {
		t.Fatalf("error = %v, want function-path guidance", err)
	}
}

func TestRenderSimpleAppPlanExplainsEphemeralState(t *testing.T) {
	plan, err := simpleapp.Resolve(simpleapp.Spec{Slug: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := renderSimpleAppPlan(&out, plan, false); code != 0 {
		t.Fatalf("render code = %d", code)
	}
	for _, want := range []string{"scale to zero", "ephemeral", "durable state", "No remote state changed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q: %s", want, out.String())
		}
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
	applySimpleAppPlanToCreateRequest(&req, plan)
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
	applyDeployLifecycleToCreateRequest(&req, "", "", 0, 0)
	if req.ExecutionMode != "request" || req.RestartPolicy != "on-failure" || req.StartupDeadlineS != 30 || req.MaxRetries != 2 {
		t.Fatalf("omitted flags changed plan defaults: %+v", req)
	}
	applyDeployLifecycleToCreateRequest(&req, "service", "always", 45, 5)
	if req.ExecutionMode != "service" || req.RestartPolicy != "always" || req.StartupDeadlineS != 45 || req.MaxRetries != 5 {
		t.Fatalf("explicit flags not applied: %+v", req)
	}
}
