package main

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRecoveryPollingHealthObservations(t *testing.T) {
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		failure bool
		alert   string
		want    string
	}{
		{"no observations", false, "", "no_observed_failure"},
		{"intent cleared error", false, "failed", "unknown_pending_successful_poll"},
		{"successful poll", false, "recovered", "no_observed_failure"},
		{"current error overrides old recovery", true, "recovered", "failing | invalid_polling_data | attempts 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := api.WorkflowRecoveryPolicyResponse{}
			if tc.failure {
				policy.AutomaticExecution = &api.WorkflowRecoveryPolicyAutomaticExecution{PollingFailure: &api.WorkflowRecoveryAutomaticPollingFailure{Code: "invalid_polling_data", Attempts: 3, FailedAt: at, RetryAt: at.Add(time.Minute)}}
			}
			if tc.alert != "" {
				policy.PollingNotification = &api.WorkflowRecoveryPollingNotification{Status: "queued", CapturedAt: at, ReceiverWebhookIDs: []string{"receiver"}, Payload: api.WorkflowRecoveryPollingWebhookPayload{State: tc.alert, InspectionURL: "/v1/apps/consumer/recovery-policy"}}
			}
			out := captureAutomationStdout(t)
			printRecoveryPollingHealth(policy)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("missing %q: %s", tc.want, out)
			}
			if tc.failure && !strings.Contains(out.String(), "next retry 2026-10-13T12:01:00Z") {
				t.Fatalf("missing retry: %s", out)
			}
			if tc.alert != "" && (!strings.Contains(out.String(), "capture does not confirm delivery") || !strings.Contains(out.String(), "Polling inspection: /v1/apps/consumer/recovery-policy")) {
				t.Fatalf("missing capture distinction or inspection: %s", out)
			}
		})
	}
}
