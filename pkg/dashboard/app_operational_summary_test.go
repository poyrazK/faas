package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestAppDetailOperationalSummaryPreservesUnknownAndPendingRecovery(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	operational := api.AppOperationalSummary{Version: 1,
		Monitoring: api.AppOperationalMonitoring{Available: true, Status: "unknown", Reason: "insufficient_requests", CheckedAt: &at,
			Incident: &api.AppOperationalIncident{ID: "incident-id", DeploymentID: "serving-release", OpenedAt: at}},
		Recovery: api.AppOperationalRecovery{RollbacksAvailable: true, RollbacksTruncated: true,
			Rollbacks:         []api.AppOperationalRollback{{ID: "rollback-id", Status: "blocked", Scope: "default", TargetDeploymentID: "previous-release"}},
			RestartsAvailable: true, Restarts: []api.RuntimeConfigRestartStatusResponse{{WakeID: "restart-id", Status: "retrying", FailureReason: "requests_active"}}},
		Recommendations: []api.AppOperationalRecommendation{{Severity: "warning", Message: "Recovery needs attention.", Next: "Inspect the pending operation."}},
	}
	recorder := httptest.NewRecorder()
	page := dashboard.Page{Title: "demo", Body: "app_detail", Data: dashboard.AppDetailData{
		App: dashboard.AppListItem{Slug: "demo", Status: "active"}, Operational: &operational,
	}}
	if err := dashboard.Render(recorder, slog.New(slog.NewTextHandler(io.Discard, nil)), "operations-nonce", page); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Current operations", "unknown", "insufficient_requests", "2026-10-07T12:00:00Z", "incident-id", "Rollback blocked", "previous-release", "/dashboard/apps/demo/restarts/restart-id", "Waiting for active requests to finish", "this list is truncated", "Recovery needs attention."} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in rendered summary", want)
		}
	}
	if strings.Contains(body, "No pending rollback operations") || strings.Contains(body, "No pending or failed restart handoffs") || strings.Contains(body, "Rollback completed") {
		t.Fatal("unknown or pending evidence was presented as complete")
	}
}
