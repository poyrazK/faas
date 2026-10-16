package safedeploy

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestOrchestratorCompleteOnly pins the #1926 mode meterd runs when the
// safe-release tokens are unset: finished rollouts complete, nothing else
// moves.
func TestOrchestratorCompleteOnly(t *testing.T) {
	stale := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		mutate    func(*state.Deployment)
		wantState string
	}{
		{"pending without a ladder completes", func(d *state.Deployment) {
			d.CanaryPreset, d.CanaryTotalSteps = "none", 0
		}, "complete"},
		{"pending ladder already at its last step completes", func(d *state.Deployment) {
			d.CanaryStep, d.CanaryTotalSteps = 4, 4
		}, "complete"},
		{"pending ladder is not started", func(d *state.Deployment) {
			d.CanaryStep, d.CanaryTotalSteps = 0, 4
		}, "pending"},
		{"rolling_out at its last step completes", func(d *state.Deployment) {
			d.RolloutState, d.CanaryStep, d.CanaryTotalSteps = "rolling_out", 4, 4
		}, "complete"},
		{"stalled rolling_out is left alone", func(d *state.Deployment) {
			d.RolloutState, d.CanaryStep, d.CanaryTotalSteps = "rolling_out", 1, 4
			d.CanaryStepStartedAt = &stale
		}, "rolling_out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubStore()
			d := seedDeployment(store, t, tt.mutate)
			orch := NewOrchestrator(store, discardLog(), "meterd:test", "")
			orch.CompleteOnly = true
			orch.Now = func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }
			stats, _, err := orch.Once(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			got := store.rollouts[d.ID]
			store.mu.Unlock()
			if got.RolloutState != tt.wantState {
				t.Fatalf("rollout_state = %q, want %q", got.RolloutState, tt.wantState)
			}
			if tt.wantState == "complete" && got.RolloutCompletedAt == nil {
				t.Fatal("rollout_completed_at must be stamped")
			}
			if stats.Started != 0 || stats.StuckDetected != 0 || stats.AutoAborted != 0 {
				t.Fatalf("completion-only mode must not start, detect or abort: %+v", stats)
			}
		})
	}
}
