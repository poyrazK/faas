package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationHealthOverviewCLI(t *testing.T) {
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy/health" {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryPolicyHealth{Status: "inactive", Overview: &api.WorkflowRecoveryOverview{Status: "needs_attention", MonitoringStatus: "failing", ManualRetryAwaitingReview: 1, ManualRetryUnresolved: 2, ManualUnresolved: 1, ManualPollingFailures: 1, Truncated: true, Issues: []api.WorkflowRecoveryOverviewIssue{{Scope: "manual_retry", Reason: "review_inspection_required", ReviewID: "retryreview", InspectionURL: "/v1/apps/consumer/recovery-policy/reviews/retryreview/status"}, {Scope: "manual_initial", Reason: "invalid_recovery_identity", ReviewID: "review", InspectionURL: "/v1/apps/consumer/initial-recovery-reviews/review"}}, LastAlert: &api.WorkflowRecoveryOverviewAlert{DeliveryStatus: "failed", Expected: 2, Dead: 1, Missing: 1}}})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsHealth([]string{"--app", "consumer"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Recovery overview: needs_attention | monitoring failing | incomplete true", "unresolved: 1 | polling failures: 1", "polling alert delivery: failed", "dead 1 | missing 1", "Inspect: /v1/apps/consumer/initial-recovery-reviews/review", "Inspect command: gregale automation failure-recovery review --app consumer --review review", "Manual retry reviews awaiting inspection: 1 | approved awaiting completion observation: 2", "Inspect command: gregale automation recovery-policy-reviews --app consumer --review retryreview --status"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
}
