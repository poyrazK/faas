package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresQueriesReadsWindowAndRanksWork(t *testing.T) {
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/databases/"+restoreSwitchDB+"/query-insights" ||
			r.URL.Query().Get("since_seconds") != "1800" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresQueryInsights{
			DatabaseID: restoreSwitchDB, Enabled: true, Since: at.Add(-30 * time.Minute),
			ObservedThrough: &at, ObservedSeconds: 900, LastSampleAt: &at,
			Queries: []api.ManagedPostgresQueryInsight{{QueryID: "123", BootAt: at.Add(-time.Hour),
				Calls: 60, TotalExecutionMS: 180, MeanExecutionMS: 3}},
		})
	})
	code, out, stderr := runSwitchCLI(t, server, "postgres", "queries", restoreSwitchDB, "--since", "30m")
	if code != 0 || !strings.Contains(out, "Observed 15m0s") || !strings.Contains(out, "60 calls  180.00 ms total  3.00 ms mean") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
