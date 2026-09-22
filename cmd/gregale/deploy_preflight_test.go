package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

func TestDeployPreflightSourceMakesDirtyCommitSelectionExplicit(t *testing.T) {
	prov := &zeroConfigProvenance{SHA: "1234567890abcdef", Dirty: true}
	source, changes := deployPreflightSource(prov, false, 3, "", "", "", "")
	if source != "commit 1234567" {
		t.Fatalf("source = %q, want commit 1234567", source)
	}
	if changes != "3 local changes excluded; use --worktree to include them" {
		t.Fatalf("local changes = %q", changes)
	}
}

func TestDeployPreflightSourceNamesIncludedWorkingTreeChanges(t *testing.T) {
	prov := &zeroConfigProvenance{SHA: "1234567890abcdef", Dirty: true}
	source, changes := deployPreflightSource(prov, true, 1, "", "", "", "")
	if source != "working tree at 1234567" {
		t.Fatalf("source = %q", source)
	}
	if changes != "1 local change included" {
		t.Fatalf("local changes = %q", changes)
	}
}

func TestRenderDeployPreflightShowsActionableRuntimePlan(t *testing.T) {
	var out bytes.Buffer
	renderDeployPreflight(&out, deployPreflightSummary{
		Slug:         "payments-api",
		Source:       "commit 1234567",
		LocalChanges: "2 local changes excluded; use --worktree to include them",
		Environment:  "production",
		BuildPlan: &api.BuildPlan{
			Class: "app", Framework: "fastapi", Version: "3.13",
			Entrypoint: "uvicorn app:app --host 0.0.0.0 --port $PORT",
			Port:       8000, HealthPath: "/healthz",
		},
		SimpleAppPlan: &simpleapp.Plan{
			ResourceProfile: "small", ExecutionMode: api.ExecutionModeRequest, ScaleToZero: true,
		},
		Release: "safe · balanced 1% -> 10% -> 50% -> 100% · automatic rollback",
	})

	for _, want := range []string{
		"Deployment plan:",
		"app:           payments-api",
		"source:        commit 1234567",
		"local changes: 2 local changes excluded; use --worktree to include them",
		"runtime:       app · fastapi · 3.13",
		"start:         uvicorn app:app --host 0.0.0.0 --port $PORT",
		"listener:      :8000 · health GET /healthz",
		"resources:     small · request · scale-to-zero",
		"environment:   production",
		"release:       safe · balanced 1% -> 10% -> 50% -> 100% · automatic rollback",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preflight missing %q in:\n%s", want, out.String())
		}
	}
}

func TestRenderDeployPreflightFunctionOmitsAppListener(t *testing.T) {
	var out bytes.Buffer
	renderDeployPreflight(&out, deployPreflightSummary{
		Slug:   "image-resize",
		Source: "template function-node",
		BuildPlan: &api.BuildPlan{
			Class: "function", Framework: "node", Runtime: "node22", Handler: "handler.handler",
		},
		Release: "standard · 100% after readiness",
	})
	got := out.String()
	for _, want := range []string{"runtime:       function · node · node22", "handler:       handler.handler"} {
		if !strings.Contains(got, want) {
			t.Errorf("preflight missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "listener:") {
		t.Fatalf("function preflight invented listener:\n%s", got)
	}
	if !strings.Contains(got, "environment:   default") {
		t.Fatalf("function preflight omitted default environment:\n%s", got)
	}
}
