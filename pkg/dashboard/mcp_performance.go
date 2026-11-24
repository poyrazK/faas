package dashboard

import (
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/mcpprotocol"
	"github.com/onebox-faas/faas/pkg/promql"
)

type MCPPerformanceView struct {
	Error, LastEvent, Freshness string
	Coverage                    string
	Tools                       []MCPToolPerformanceView
	Charts                      []MCPPerformanceChart
	// Insights shows argument mismatches and estimated compute per tool
	// (ADR-20261122194324, mcp_tool_insights).
	Insights bool
}
type MCPToolPerformanceView struct {
	Tool, Calls, Errors, Cancellations, P50, P95, P99, StreamP95 string
	Mismatches, Compute                                          string
}
type MCPPerformanceChart struct {
	Title, Unit string
	Points      []MCPPerformancePoint
	Peak        string
}
type MCPPerformancePoint struct {
	X, Y, Height float64
	Label        string
}

func MCPPerformance(snapshot mcpprotocol.Performance, now time.Time) MCPPerformanceView {
	view := MCPPerformanceView{Coverage: "Unavailable", LastEvent: snapshot.LastEvent.Format(time.RFC3339), Freshness: "Recent observations"}
	if now.Sub(snapshot.LastEvent) > 5*time.Minute {
		view.Freshness = "No recent observations; traffic may be idle or telemetry missing"
	}
	latency := func(v *float64) string {
		if v == nil {
			return "Insufficient samples"
		}
		return fmt.Sprintf("%.1f ms", *v*1000)
	}
	for _, r := range snapshot.Tools {
		view.Tools = append(view.Tools, MCPToolPerformanceView{Tool: r.Tool, Calls: fmt.Sprintf("%.0f", r.Calls), Errors: fmt.Sprintf("%.0f", r.Errors), Cancellations: fmt.Sprintf("%.0f", r.Cancellations), P50: latency(r.P50), P95: latency(r.P95), P99: latency(r.P99), StreamP95: latency(r.StreamP95)})
	}
	view.Charts = []MCPPerformanceChart{mcpChart("RPC traffic", "calls/s", snapshot.Traffic, now, 1), mcpChart("RPC and tool failures", "failures/s", snapshot.Errors, now, 1), mcpChart("Execution p95", "ms", snapshot.Latency, now, 1000)}
	return view
}

// MCPToolInsights adds argument mismatches and estimated compute at memoryMB
// of plan RAM to a view built from snapshot.
func MCPToolInsights(view *MCPPerformanceView, snapshot mcpprotocol.Performance, memoryMB int) {
	view.Insights = true
	for i, r := range snapshot.Tools {
		if i >= len(view.Tools) || view.Tools[i].Tool != r.Tool {
			return
		}
		estimate := mcpprotocol.EstimateToolCompute(r.ExecutionSeconds, memoryMB)
		view.Tools[i].Mismatches = fmt.Sprintf("%.0f", r.Mismatches)
		view.Tools[i].Compute = fmt.Sprintf("%.1f GB-s · ≤ $%d.%05d", estimate.GBSeconds, estimate.Millicents/100_000, estimate.Millicents%100_000)
	}
}

func mcpChart(title, unit string, samples []promql.QueryRangeSample, now time.Time, scale float64) MCPPerformanceChart {
	chart := MCPPerformanceChart{Title: title, Unit: unit}
	peak := 0.0
	for _, s := range samples {
		if !math.IsNaN(s.Value) && !math.IsInf(s.Value, 0) && s.Value >= 0 {
			peak = max(peak, s.Value*scale)
		}
	}
	chart.Peak = fmt.Sprintf("%.2f", peak)
	for _, s := range samples {
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) || s.Value < 0 || s.Timestamp < now.Add(-time.Hour).Unix() || s.Timestamp > now.Unix() {
			continue
		}
		height := 0.0
		if peak > 0 {
			height = 100 * s.Value * scale / peak
		}
		chart.Points = append(chart.Points, MCPPerformancePoint{X: float64(s.Timestamp-now.Add(-time.Hour).Unix()) / 60 * 5, Y: 110 - height, Height: height, Label: fmt.Sprintf("%s: %.2f %s", time.Unix(s.Timestamp, 0).UTC().Format("15:04"), s.Value*scale, unit)})
	}
	return chart
}
