package apidserver

// adr: 20261123110137 — per-tool MCP insights API.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcpprotocol"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPToolInsightsAPI(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "mcp-insights@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "insights-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "query_range") {
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	}))
	defer backend.Close()
	srv := &server{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil)), promqlClient: promql.NewClient(backend.URL, backend.Client())}
	get := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/apps/insights-app/mcp-tool-insights", nil)
		r.SetPathValue("slug", "insights-app")
		w := httptest.NewRecorder()
		srv.getMCPToolInsights(w, r, acct)
		return w
	}
	if w := get(); w.Code != http.StatusForbidden {
		t.Fatalf("flag off: status=%d body=%s", w.Code, w.Body.String())
	}
	srv.seedPlatformFlag(platformflags.MCPToolInsights, platformflags.FromAccountList(true, true, nil))
	w := get()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp api.MCPToolInsightsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.App != app.Slug || resp.Telemetry != api.MCPToolInsightsTelemetryNotObserved || resp.Tools == nil || len(resp.Tools) != 0 || resp.MemoryMB <= 0 {
		t.Fatalf("no telemetry must read as not_observed with an empty list: %+v", resp)
	}
}

func TestMCPToolInsightRow(t *testing.T) {
	p95 := 0.25
	row := mcpToolInsight(mcpprotocol.ToolPerformance{Tool: "search", Calls: 10, Errors: 2, Mismatches: 1, ExecutionSeconds: 3600, P95: &p95}, 1016)
	// 3600 s at 1016+8 MB = 3600 GB-s = 1 GB-h at the overage rate.
	if row.GBSeconds != 3600 || row.EstimatedMillicents != api.OverageMillicentsPerGBHour || row.P95MS == nil || *row.P95MS != 250 || row.P50MS != nil {
		t.Fatalf("row = %+v", row)
	}
}
