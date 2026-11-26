package apidserver_test

// adr: 20261123110137 — the MCP tool insights route is flag-gated and owned.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPToolInsightsRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "insights-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
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
	*srv.PromqlClient() = promql.NewClient(backend.URL, backend.Client())

	if w := e.Do(t, "GET", "/v1/apps/insights-app/mcp-tool-insights", nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("flag off: status=%d body=%s", w.Code, w.Body.String())
	}
	srv.EnablePlatformFlagForAll(platformflags.MCPToolInsights)
	w := e.Do(t, "GET", "/v1/apps/insights-app/mcp-tool-insights", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp api.MCPToolInsightsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Telemetry != api.MCPToolInsightsTelemetryNotObserved || resp.App != "insights-app" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if w := e.Do(t, "GET", "/v1/apps/someone-else/mcp-tool-insights", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown app: status=%d", w.Code)
	}
}
