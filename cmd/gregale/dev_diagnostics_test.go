package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevDiagnosticFromDeploymentUsesCatalogAndContext(t *testing.T) {
	d := devDiagnosticFromDeployment(api.DeploymentResponse{
		ID:        "dep-123",
		ErrorCode: api.CodeStageReadinessFailed,
		Error:     "health endpoint did not respond",
	}, "readiness", "health timeout")

	if d.Code != api.CodeStageReadinessFailed || d.Phase != "readiness" {
		t.Fatalf("diagnostic = %+v, want readiness stage diagnostic", d)
	}
	if !d.Catalog || d.Hint == "" || d.Fix == "" {
		t.Fatalf("diagnostic did not use catalog prose: %+v", d)
	}
	if d.DeploymentID != "dep-123" || d.LogsCommand != "gregale logs --deployment dep-123" {
		t.Fatalf("diagnostic context = %+v", d)
	}
}

func TestDevDiagnosticFromErrorClassifiesAPIProblem(t *testing.T) {
	err := &api.APIError{Problem: api.Problem{
		Code:   api.CodeAppLoopbackBound,
		Detail: "listener bound to 127.0.0.1",
	}}
	d := devDiagnosticFromError(err, "readiness")
	if d.Code != api.CodeAppLoopbackBound || d.Hint == "" || d.Fix == "" {
		t.Fatalf("diagnostic = %+v, want catalog-backed loopback guidance", d)
	}
}

func TestClassifyDevRuntimeLog(t *testing.T) {
	tests := []struct {
		name string
		line string
		code string
	}{
		{name: "module", line: "Error: Cannot find module 'express'", code: devDiagImportFailure},
		{name: "bind", line: "listen EADDRINUSE: address already in use", code: devDiagBindFailure},
		{name: "upstream", line: "upstream connection refused", code: devDiagUpstreamFailure},
		{name: "oom", line: "process OOMKilled by memory limit", code: api.CodeAppRuntimeOOM},
		{name: "panic", line: "panic: nil pointer dereference", code: devDiagStartupCrash},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, ok := classifyDevRuntimeLog(test.line)
			if !ok || d.Code != test.code {
				t.Fatalf("classifyDevRuntimeLog(%q) = (%+v, %v), want code %q", test.line, d, ok, test.code)
			}
		})
	}
	if _, ok := classifyDevRuntimeLog("server listening on 0.0.0.0:8080"); ok {
		t.Fatal("normal readiness log should not emit a diagnostic")
	}
}

func TestDevDiagnosticFromTextKeepsActionableOutput(t *testing.T) {
	d := devDiagnosticFromError(errors.New("build timed out"), "image_build")
	if d.Code != api.CodeStageImageBuildTimeout || d.Phase != "build" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if !strings.Contains(d.Hint, "build") || d.Fix == "" {
		t.Fatalf("diagnostic lacks actionable prose: %+v", d)
	}
}
