package mcpinsights_test

// adr: 20261123110137 — per-tool MCP insights.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpinsights"
	"github.com/onebox-faas/faas/pkg/mcpprotocol"
	"github.com/onebox-faas/faas/pkg/promql"
)

type reader struct{ err error }

func (r reader) QueryVector(context.Context, string) ([]promql.VectorSample, error) {
	return nil, r.err
}

func (r reader) QueryRange(context.Context, string, string, string, string) ([]struct {
	Metric map[string]string
	Values []promql.QueryRangeSample
}, error) {
	return nil, r.err
}

const appID = "6f1c1a52-0f4b-4a0e-9d55-6d0b8f0b9a11"

func TestReadWithoutTelemetryIsNotObserved(t *testing.T) {
	resp, err := mcpinsights.Read(t.Context(), reader{}, "shop", appID, 512, time.Now())
	if err != nil || resp.Telemetry != api.MCPToolInsightsTelemetryNotObserved || resp.Tools == nil || len(resp.Tools) != 0 || resp.MemoryMB != 512 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestReadFailureIsAnError(t *testing.T) {
	if _, err := mcpinsights.Read(t.Context(), reader{err: errors.New("down")}, "shop", appID, 512, time.Now()); err == nil {
		t.Fatal("a failed read must not look like an idle app")
	}
}

func TestRow(t *testing.T) {
	p95 := 0.25
	row := mcpinsights.Row(mcpprotocol.ToolPerformance{Tool: "search", Calls: 10, Errors: 2, Mismatches: 1, ExecutionSeconds: 3600, P95: &p95}, 1016)
	// 3600 s at 1016+8 MB = 3600 GB-s = 1 GB-h at the overage rate.
	if row.GBSeconds != 3600 || row.EstimatedMillicents != api.OverageMillicentsPerGBHour || row.P95MS == nil || *row.P95MS != 250 || row.P50MS != nil || row.Mismatches != 1 {
		t.Fatalf("row = %+v", row)
	}
}
