package main

import (
	"bytes"
	"os"
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
