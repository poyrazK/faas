package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261102124306 step 3: debug status shows memory pressure and the RAM
// that would have kept 30% free, beside an OOM kill's own remedy.
func TestCmdDebugStatusShowsMemoryPressureAndSuggestion(t *testing.T) {
	status := api.DebugStatusResponse{
		Verdict: "degraded", Headline: "1 out-of-memory kill in the last 1h.", Since: "1h",
		Deployments: []api.DebugStatusDeployment{}, Regressions: []api.DebugRegressionItem{},
		RecentFailures: []api.DebugTelemetryRequestItem{}, OOMKills: []api.DebugStatusOOMKill{},
		OOMFix: "• upgrade from a 512 MB plan",
		MemoryPressure: &api.DebugStatusMemory{
			Episodes: 3, LowestAvailableMB: 40, TotalMB: 480, PeakAnonMB: 410, LastAt: "2026-11-05T10:00:00Z",
			CurrentMB: 512, SuggestedMB: 704, Plan: "pro", PlanMB: 1024, FitsPlan: true,
			Advice: "Raise ram_mb from 512 MB: about 704 MB would have kept 30% free, and the pro plan allows up to 1024 MB.",
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/shop/debug/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previousOut })

	if code := main.Run([]string{"debug", "status", "shop"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"Memory pressure ",
		"available fell to 40 of 480 MB (8%), 3 episodes in the last day, newest 2026-11-05 10:00 UTC\n",
		"Memory:\n  • upgrade from a 512 MB plan\n  Raise ram_mb from 512 MB: about 704 MB would have kept 30% free, and the pro plan allows up to 1024 MB.\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
}
