// Package operatorpark projects durable force-park evidence without credentials.
package operatorpark

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func Response(e *state.OperatorParkExecution) *api.OperatorParkExecutionResponse {
	if e == nil {
		return nil
	}
	r := &api.OperatorParkExecutionResponse{Phase: e.Phase, NodeID: e.Target.NodeID, WakeID: e.Target.WakeID, Generation: e.Target.Generation,
		Action: e.Target.Action, StoppedAt: e.StoppedAt, TerminalState: e.TerminalState, ColdBootRequired: e.TerminalState == string(state.StateStopped), UpdatedAt: e.UpdatedAt}
	if e.Receipt != nil {
		r.CaptureCompleted = e.Receipt.Outcome == string(state.StateParked)
		r.Reason = e.Receipt.Reason
	}
	if r.ColdBootRequired && r.CaptureCompleted && r.Reason == "" {
		r.Reason = "Captured snapshot became ineligible; cold boot required."
	}
	return r
}
