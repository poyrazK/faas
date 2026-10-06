// adr: 601 — keep checked historical recovery pinned to its serving predecessor.
package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedRollbackServiceKeepsExactNewerPredecessor(t *testing.T) {
	now := time.Now()
	rollout := state.Deployment{ID: "historical-target", AppID: "app", Scope: "default", Status: state.DeployLive, CreatedAt: now.Add(-time.Hour), RolloutState: "rolling_out", ServiceRolloutHandoff: state.ServiceRolloutHandoff{PredecessorDeploymentID: "current"}}
	current := state.Deployment{ID: "current", AppID: "app", Scope: "default", Status: state.DeployLive, CreatedAt: now, RolloutState: "complete", TrafficPercent: 100}
	unrelated := current
	unrelated.ID = "other"
	unrelated.CreatedAt = now.Add(time.Minute)
	got := previousServiceDeployment(rollout, []state.Deployment{rollout, current, unrelated})
	if got.ID != current.ID {
		t.Fatalf("historical rollback lost its exact serving predecessor: %+v", got)
	}
	for _, scenario := range []string{"missing", "wrong scope", "wrong app", "active rollout"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := current
			switch scenario {
			case "missing":
				candidate.ID = "other"
			case "wrong scope":
				candidate.Scope = "staging"
			case "wrong app":
				candidate.AppID = "other"
			case "active rollout":
				candidate.RolloutState = "rolling_out"
			}
			if got := previousServiceDeployment(rollout, []state.Deployment{candidate, unrelated}); got.ID != "" {
				t.Fatalf("replaced a missing/ineligible pinned predecessor: %+v", got)
			}
		})
	}
}
