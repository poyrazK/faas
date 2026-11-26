package dashboard

// adr: 20261122194324 — per-tool mismatches and compute estimate.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mcpprotocol"
)

func TestMCPToolInsights(t *testing.T) {
	now := time.Now()
	snapshot := mcpprotocol.Performance{LastEvent: now, Tools: []mcpprotocol.ToolPerformance{
		{Tool: "lookup_order", Calls: 40, Errors: 3, Mismatches: 2, ExecutionSeconds: 3600},
		{Tool: "other", Calls: 1},
	}}
	view := MCPPerformance(snapshot, now)
	if view.Insights || view.Tools[0].Compute != "" {
		t.Fatal("insights shown without the flag")
	}
	MCPToolInsights(&view, snapshot, 1016)
	if !view.Insights || view.Tools[0].Mismatches != "2" || view.Tools[0].Compute != "3600.0 GB-s · ≤ $0.05000" {
		t.Fatalf("tool view = %+v", view.Tools[0])
	}
	if view.Tools[1].Compute != "0.0 GB-s · ≤ $0.00000" {
		t.Fatalf("idle tool = %+v", view.Tools[1])
	}
}
