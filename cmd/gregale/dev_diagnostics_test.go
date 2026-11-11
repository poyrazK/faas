package main_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevDiagnosticFromDeploymentUsesCatalogAndContext(t *testing.T) {
	d := main.DevDiagnosticFromDeployment(api.DeploymentResponse{
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
	if d.DeploymentID != "dep-123" || d.LogsCommand != "" {
		t.Fatalf("diagnostic context = %+v", d)
	}
}

func TestDevDiagnosticFromErrorClassifiesAPIProblem(t *testing.T) {
	err := &api.APIError{Problem: api.Problem{
		Code:   api.CodeAppLoopbackBound,
		Detail: "listener bound to 127.0.0.1",
	}}
	d := main.DevDiagnosticFromError(err, "readiness")
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
		{name: "module", line: "Error: Cannot find module 'express'", code: main.DevDiagImportFailure},
		{name: "bind", line: "listen EADDRINUSE: address already in use", code: main.DevDiagBindFailure},
		{name: "upstream", line: "upstream connection refused", code: main.DevDiagUpstreamFailure},
		{name: "local database refusal", line: "Error: connect ECONNREFUSED 127.0.0.1:5432", code: main.DevDiagUpstreamFailure},
		{name: "IPv6 dependency refusal", line: "dial tcp [::1]:6379: connect: connection refused", code: main.DevDiagUpstreamFailure},
		{name: "unavailable interface", line: "listen EADDRNOTAVAIL 192.0.2.1:8080", code: main.DevDiagBindFailure},
		{name: "loopback listener", line: "listener bound to 127.0.0.1; unreachable from the gateway", code: api.CodeAppLoopbackBound},
		{name: "traceback mentioning local URL", line: "Traceback: failed request to http://127.0.0.1:8080", code: main.DevDiagStartupCrash},
		{name: "oom", line: "process OOMKilled by memory limit", code: api.CodeAppRuntimeOOM},
		{name: "panic", line: "panic: nil pointer dereference", code: main.DevDiagStartupCrash},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, ok := main.ClassifyDevRuntimeLog(test.line)
			if !ok || d.Code != test.code {
				t.Fatalf("classifyDevRuntimeLog(%q) = (%+v, %v), want code %q", test.line, d, ok, test.code)
			}
		})
	}
	for _, line := range []string{
		"server listening on 0.0.0.0:8080",
		"Local: http://127.0.0.1:5173",
		"server listening on localhost:8080",
		"server bound to 127.0.0.1:5173",
		"server bound to localhost:8080",
		"upstream healthy",
		"request from 127.0.0.1 succeeded",
	} {
		if d, ok := main.ClassifyDevRuntimeLog(line); ok {
			t.Fatalf("normal log %q emitted a diagnostic: %+v", line, d)
		}
	}
}

func TestDevDiagnosticFromTextKeepsActionableOutput(t *testing.T) {
	d := main.DevDiagnosticFromError(errors.New("build timed out"), "image_build")
	if d.Code != api.CodeStageImageBuildTimeout || d.Phase != "build" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if !strings.Contains(d.Hint, "build") || d.Fix == "" {
		t.Fatalf("diagnostic lacks actionable prose: %+v", d)
	}
}
