package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAutomationReportCLI(t *testing.T) {
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		start, _ := time.Parse(time.RFC3339Nano, q.Get("created_after"))
		end, _ := time.Parse(time.RFC3339Nano, q.Get("created_before"))
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy/report" || q.Get("workflow") != "orders" || end.Sub(start) != 7*24*time.Hour {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryReport{Totals: api.WorkflowRecoveryReportTotals{Attempts: 3, Succeeded: 2, WithoutObservedResult: 1}, CoverageNotes: []string{"retained evidence"}, Evidence: []api.WorkflowRecoveryHistoryEntry{{ID: "audit:evidence", RunID: "run", ReviewID: "review", ChildJobID: "child", OccurredAt: time.Now().UTC()}}})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomations([]string{"report", "--app", "consumer", "--workflow", "orders", "--since", "7d"}); code != 0 || !strings.Contains(out.String(), "attempts 3 | succeeded 2") || !strings.Contains(out.String(), "audit:evidence") || !strings.Contains(out.String(), "retained evidence") {
		t.Fatalf("report %d %s", code, out)
	}
	for _, args := range [][]string{{}, {"--app", "a", "--since", "31d"}, {"--app", "a", "--since", "0h"}, {"--app", "a", "--since", "7d", "--created-after", "2026-10-01T00:00:00Z"}, {"--app", "a", "--policy-version", "-1"}} {
		if code := main.CmdAutomationsReport(args); code != 1 {
			t.Fatalf("invalid %+v %d", args, code)
		}
	}
}
func TestAutomationReportDuration(t *testing.T) {
	for value, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "24h": 24 * time.Hour, "30d": 30 * 24 * time.Hour} {
		got, err := main.RecoveryReportDuration(value)
		if err != nil || got != want {
			t.Fatalf("%s %s %v", value, got, err)
		}
	}
	for _, value := range []string{"", "999999999999999999999999d", "-1d", "1.5d", "0d", "31d", "721h"} {
		if _, err := main.RecoveryReportDuration(value); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}
