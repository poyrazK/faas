package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderDebugRegressionsTableShowsKindAndErrors(t *testing.T) {
	var out bytes.Buffer
	main.RenderDebugRegressionsTable(&out, api.DebugRegressionsResponse{Regressions: []api.DebugRegressionItem{
		{DeploymentID: "dep-81", Route: "POST /pay", Kind: "errors", State: "active", Factor: "1.00", ErrorRatePct: 12.5, ErrorRateBasePct: 0.4, AffectedCount: 61},
		{DeploymentID: "dep-81", Route: "GET /", State: "active", Factor: "1.50", P95MS: 300, P95BaseMS: 200, AffectedCount: 120},
	}})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "KIND") || !strings.Contains(lines[0], "ERRORS") {
		t.Fatalf("table = %q", out.String())
	}
	if !strings.Contains(lines[1], "errors") || !strings.Contains(lines[1], "0.4→12.5%") {
		t.Fatalf("errors row = %q", lines[1])
	}
	// An older server sends no kind: the row reads as a latency regression.
	if fields := strings.Fields(lines[2]); fields[3] != "latency" || !strings.Contains(lines[2], " - ") {
		t.Fatalf("legacy row = %q", lines[2])
	}
}
