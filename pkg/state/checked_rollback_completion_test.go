package state_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestCheckedRollbackCompletionAuthenticatesHandoff(t *testing.T) {
	r := api.RollbackOperation{ID: "request", AppID: "app", Scope: "default", TargetDeploymentID: "target", CurrentDeploymentID: "previous", Status: "routing", AuditID: "audit", Service: true}
	for _, tc := range []struct {
		name   string
		change func(*state.ServiceRolloutHandoff)
		want   bool
	}{
		{name: "superseded_after_success", want: true},
		{name: "another_request", change: func(h *state.ServiceRolloutHandoff) { h.BindingsCheck.RequestID = "another" }},
		{name: "another_audit", change: func(h *state.ServiceRolloutHandoff) { h.BindingsCheck.AuditID = "another" }},
		{name: "another_predecessor", change: func(h *state.ServiceRolloutHandoff) { h.PredecessorDeploymentID = "another" }},
		{name: "aborted", change: func(h *state.ServiceRolloutHandoff) { h.Action = state.ServiceRolloutActionAbort }},
		{name: "still_draining", change: func(h *state.ServiceRolloutHandoff) { h.Phase = state.ServiceRolloutPhaseDraining }},
		{name: "unqualified", change: func(h *state.ServiceRolloutHandoff) { h.BindingsCheck.Status = "pending" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := state.ServiceRolloutHandoff{Action: state.ServiceRolloutActionPromote, Phase: state.ServiceRolloutPhaseComplete, PredecessorDeploymentID: r.CurrentDeploymentID, BindingsCheck: &api.ServiceRolloutBindingGate{RequestID: r.ID, Action: state.ServiceRolloutActionPromote, DeploymentID: r.TargetDeploymentID, Status: "passed", AuditID: r.AuditID}}
			if tc.change != nil {
				tc.change(&h)
			}
			d := state.Deployment{ID: r.TargetDeploymentID, AppID: r.AppID, Scope: r.Scope, Status: state.DeploySuperseded, TrafficPercent: 0, ServiceRolloutHandoff: h}
			if got := state.CheckedRollbackCompleted(r, d); got != tc.want {
				t.Fatalf("completion=%v want=%v", got, tc.want)
			}
		})
	}
}
