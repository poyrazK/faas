package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Latency covers successful calls: an edge whose every call failed prints no
// latency instead of p50=0.0ms (prod findings 2026-11-15 #37).
func TestRenderServiceMap_AllFailedEdgeHasNoLatency(t *testing.T) {
	var buf bytes.Buffer
	main.RenderServiceMap(&buf, api.ServiceMapResponse{Range: "1h", Edges: []api.ServiceMapEdge{
		{CallerAppSlug: "frontend", TargetAppSlug: "currency", Calls: 40, Errors: 40, ErrorRatePct: 100},
		{CallerAppSlug: "frontend", TargetAppSlug: "cart", Calls: 40, Errors: 1, ErrorRatePct: 2.5, LatencyP50MS: 12, LatencyP95MS: 30},
	}})
	out := buf.String()
	if !strings.Contains(out, "Latency:    none (no successful calls in window)") || strings.Contains(out, "p50=0.0ms") {
		t.Fatalf("all-failed edge latency:\n%s", out)
	}
	if !strings.Contains(out, "Latency:    p50=12.0ms p95=30.0ms") {
		t.Fatalf("healthy edge latency missing:\n%s", out)
	}
}
