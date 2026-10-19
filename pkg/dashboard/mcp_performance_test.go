package dashboard

import (
	"io"
	"log/slog"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mcpprotocol"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestMCPPerformanceGapsAndFreshness(t *testing.T) {
	now := time.Now().UTC()
	value := .25
	view := MCPPerformance(mcpprotocol.Performance{LastEvent: now.Add(-10 * time.Minute), Tools: []mcpprotocol.ToolPerformance{{Tool: "greet", Calls: 50, P95: &value}}, Traffic: []promql.QueryRangeSample{{Timestamp: now.Unix(), Value: 1}, {Timestamp: now.Add(-time.Minute).Unix(), Value: math.NaN()}}}, now)
	if view.Freshness == "Recent observations" || view.Tools[0].P95 != "250.0 ms" || view.Tools[0].P50 != "Insufficient samples" || len(view.Charts[0].Points) != 1 || len(view.Charts[2].Points) != 0 {
		t.Fatalf("bad view: %+v", view)
	}
}

func TestMCPPerformanceChartRendering(t *testing.T) {
	now := time.Now().UTC()
	view := MCPPerformance(mcpprotocol.Performance{LastEvent: now, Traffic: []promql.QueryRangeSample{{Timestamp: now.Unix(), Value: 2}}}, now)
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", Page{Title: "MCP", Body: "mcp_operations", Data: MCPOperationsData{Performance: &view}}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{"<svg", "RPC traffic", "No chart samples available", "Recent observations", "Execution and SSE delivery durations are measured separately"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}
