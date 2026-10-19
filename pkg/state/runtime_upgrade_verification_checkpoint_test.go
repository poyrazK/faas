package state_test

// adr: 694

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestRuntimeUpgradeVerificationCheckpointSeparatesProgressAndHistory(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		status, reason string
		deadline       time.Time
		phase          state.RuntimeUpgradeVerificationPhase
		finalReason    string
	}{
		{"pending", "gateway_confirmation_pending", now.Add(time.Minute), state.RuntimeUpgradeVerificationPending, "gateway_confirmation_pending"},
		{"pending", "candidate_health_unconfirmed", now.Add(time.Minute), state.RuntimeUpgradeVerificationPending, "candidate_health_unconfirmed"},
		{"pending", "activation_inputs_changed", now.Add(time.Minute), state.RuntimeUpgradeVerificationBlocked, "activation_inputs_changed"},
		{"pending", "health_scope_unsupported", now.Add(time.Minute), state.RuntimeUpgradeVerificationBlocked, "health_scope_unsupported"},
		{"verified", "", now.Add(time.Minute), state.RuntimeUpgradeVerificationVerified, ""},
		{"verified", "", now, state.RuntimeUpgradeVerificationExpired, "deadline_exceeded"},
	} {
		t.Run(string(tc.phase)+"_"+tc.reason, func(t *testing.T) {
			j := state.RuntimeUpgradeVerificationJournal{Phase: state.RuntimeUpgradeVerificationPending, DeadlineAt: tc.deadline, LeaseToken: "private", LeaseUntil: now.Add(time.Minute)}
			out := state.RuntimeUpgradeVerification{Status: tc.status, Reason: tc.reason, CheckedAt: now}
			got := state.RuntimeUpgradeVerificationCheckpoint(j, out, now)
			if got.Phase != tc.phase || got.Reason != tc.finalReason || got.LeaseToken != "" || !got.LeaseUntil.IsZero() || (got.FinishedAt.IsZero()) != (tc.phase == state.RuntimeUpgradeVerificationPending) || got.LastObservation.Status != tc.status {
				t.Fatal(got)
			}
		})
	}
}
