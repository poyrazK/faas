// Package operatorrestart projects durable operator restart evidence for polling.
package operatorrestart

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func Response(execution *state.OperatorRestartExecution) *api.OperatorRestartExecutionResponse {
	if execution == nil {
		return nil
	}
	return &api.OperatorRestartExecutionResponse{Phase: execution.Phase, NodeID: execution.Target.NodeID,
		WakeID: execution.Target.WakeID, Generation: execution.Target.Generation,
		SnapshotIDs: slices.Clone(execution.Target.SnapshotIDs), DestroyedAt: execution.DestroyedAt, UpdatedAt: execution.UpdatedAt}
}
