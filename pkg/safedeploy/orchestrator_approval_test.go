package safedeploy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 — a release waiting for approval is not stuck.

type holdStore struct {
	*stubStore
	hold json.RawMessage
}

func (s holdStore) LatestRolloutHold(context.Context, string) (json.RawMessage, error) {
	return s.hold, nil
}

func TestOrchestrator_LeavesReleasesWaitingForApprovalToTheirTimeout(t *testing.T) {
	hold := func(rule string, age time.Duration) json.RawMessage {
		raw, _ := json.Marshal(api.ManualRolloutDecision(rule, "waiting for 1 more approval(s)", 0, 1, time.Now().Add(-age)))
		return raw
	}
	for _, tc := range []struct {
		name      string
		hold      json.RawMessage
		aborted   int
		awaiting  int
		detecting int
	}{
		{name: "fresh approval hold", hold: hold(api.RolloutRuleHoldAwaitingApproval, time.Minute), awaiting: 1},
		{name: "stale approval hold", hold: hold(api.RolloutRuleHoldAwaitingApproval, 2*StuckAfterDuration), aborted: 1, detecting: 1},
		{name: "other hold", hold: hold(api.RolloutRuleHoldInsufficient, time.Minute), aborted: 1, detecting: 1},
		{name: "no hold", aborted: 1, detecting: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStubStore()
			stuckAt := time.Now().Add(-2 * StuckAfterDuration)
			dep := seedDeployment(stub, t, func(d *state.Deployment) {
				d.RolloutState = "rolling_out"
				d.CanaryStepStartedAt = &stuckAt
				d.RolloutStartedAt = &stuckAt
			})
			recovery := &rolloutRecoveryStub{}
			o := NewOrchestrator(holdStore{stubStore: stub, hold: tc.hold}, discardLog(), "meterd:safedeploy", "")
			o.Targets = rolloutTargetStub{app: state.App{ID: dep.AppID, Slug: "demo-app"}}
			o.Recovery = recovery
			stats, _, err := o.Once(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if stats.AwaitingApproval != tc.awaiting || stats.AutoAborted != tc.aborted || stats.StuckDetected != tc.detecting || recovery.calls != tc.aborted {
				t.Fatalf("stats = %+v, recovery calls = %d", stats, recovery.calls)
			}
		})
	}
}
