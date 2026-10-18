package mcpprotocol

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/promql"
)

const MinSamples = 20

// ToolPerformance exposes only the reviewed tool vocabulary. Percentiles are
// approximate histogram estimates and are absent below the traffic threshold.
type ToolPerformance struct {
	Tool                                           string
	Calls, Errors, Cancellations, ExecutionSamples float64
	P50, P95, P99, StreamP95                       *float64
}
type Performance struct {
	LastEvent                time.Time
	Tools                    []ToolPerformance
	Traffic, Errors, Latency []promql.QueryRangeSample
}
type Reader interface {
	QueryVector(context.Context, string) ([]promql.VectorSample, error)
	QueryRange(context.Context, string, string, string, string) ([]struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}, error)
}

// Fetch performs a fixed set of app-scoped queries, shared by dashboard reads.
// Callers must resolve app ownership before passing its UUID. Missing sources
// and incomplete reads are errors, never healthy zero-valued snapshots.
func Fetch(ctx context.Context, reader Reader, appID string, now time.Time) (Performance, error) {
	var result Performance
	if _, err := uuid.Parse(appID); err != nil || reader == nil {
		return result, fmt.Errorf("MCP performance unavailable")
	}
	selector := fmt.Sprintf(`{app_id=%q}`, appID)
	calls := `sum by (tool) (increase(vmmd_mcp_protocol_requests_total` + selector + `[1h]))`
	failures := fmt.Sprintf(`sum by (tool) (increase(vmmd_mcp_protocol_requests_total{app_id=%q,outcome=~"rpc_error|tool_error|authorization_error|internal_error|timeout"}[1h]))`, appID)
	cancellations := `sum by (tool) (increase(vmmd_mcp_protocol_cancellations_total` + selector + `[1h]))`
	executionCount := `sum by (tool) (increase(vmmd_mcp_protocol_execution_seconds_count` + selector + `[1h]))`
	quantile := func(q string, metric string) string {
		return `histogram_quantile(` + q + `,sum by (tool,le) (increase(` + metric + `_bucket` + selector + `[1h])))`
	}
	queries := []string{`max(vmmd_mcp_protocol_last_event_timestamp_seconds` + selector + `)`, calls, failures, cancellations, executionCount, quantile("0.5", "vmmd_mcp_protocol_execution_seconds"), quantile("0.95", "vmmd_mcp_protocol_execution_seconds"), quantile("0.99", "vmmd_mcp_protocol_execution_seconds"), quantile("0.95", "vmmd_mcp_protocol_stream_seconds"), `sum by (tool) (increase(vmmd_mcp_protocol_stream_seconds_count` + selector + `[1h]))`}
	rows := make([][]promql.VectorSample, len(queries))
	errs := make([]error, len(queries)+3)
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func() { defer wg.Done(); rows[i], errs[i] = reader.QueryVector(ctx, q) }()
	}
	charts := []string{`sum(rate(vmmd_mcp_protocol_requests_total` + selector + `[5m]))`, fmt.Sprintf(`sum(rate(vmmd_mcp_protocol_requests_total{app_id=%q,outcome=~"rpc_error|tool_error|authorization_error|internal_error|timeout"}[5m]))`, appID), `histogram_quantile(0.95,sum by (le) (rate(vmmd_mcp_protocol_execution_seconds_bucket` + selector + `[5m]))) and on() (sum(increase(vmmd_mcp_protocol_execution_seconds_count` + selector + `[5m])) >= 20)`}
	chartRows := make([][]promql.QueryRangeSample, 3)
	for i, q := range charts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var values []struct {
				Metric map[string]string
				Values []promql.QueryRangeSample
			}
			values, errs[len(queries)+i] = reader.QueryRange(ctx, q, fmt.Sprint(now.Add(-time.Hour).Unix()), fmt.Sprint(now.Unix()), "60")
			if len(values) == 1 && len(values[0].Values) <= 61 {
				chartRows[i] = values[0].Values
			} else if len(values) > 0 {
				errs[len(queries)+i] = fmt.Errorf("MCP chart exceeds bounds")
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return Performance{}, fmt.Errorf("MCP performance unavailable")
		}
	}
	if len(rows[0]) != 1 || !finite(rows[0][0].Value) || rows[0][0].Value <= 0 || rows[0][0].Value > float64(now.Add(time.Minute).Unix()) {
		return Performance{}, fmt.Errorf("MCP telemetry not observed")
	}
	result.LastEvent = time.Unix(int64(rows[0][0].Value), 0).UTC()
	for _, tool := range tools {
		value := func(i int) *float64 {
			for _, r := range rows[i] {
				if r.Labels["tool"] == tool && finite(r.Value) && r.Value >= 0 {
					v := r.Value
					return &v
				}
			}
			return nil
		}
		count := value(1)
		if count == nil {
			continue
		}
		row := ToolPerformance{Tool: tool, Calls: *count}
		if v := value(2); v != nil {
			row.Errors = *v
		}
		if v := value(3); v != nil {
			row.Cancellations = *v
		}
		if v := value(4); v != nil {
			row.ExecutionSamples = *v
			if *v >= MinSamples {
				row.P50 = value(5)
				row.P95 = value(6)
				row.P99 = value(7)
			}
		}
		if v := value(9); v != nil && *v >= MinSamples {
			row.StreamP95 = value(8)
		}
		result.Tools = append(result.Tools, row)
	}
	result.Traffic = chartRows[0]
	result.Errors = chartRows[1]
	result.Latency = chartRows[2]
	return result, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
