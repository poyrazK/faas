package sched

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// primeAwaitsCapacity reports whether a snapshot prime that node budgets
// refused should wait for capacity instead of failing the deployment. It does
// while the deployment is still in the snapshot_prepare attempt that failed
// and that stage began less than api.SnapshotPrimeCapacityMaxWait ago. The
// refusal happens before any instance exists, so nothing is left to clean up:
// the deployment stays snapshotting and prime recovery (prime_recovery.go)
// dispatches it again once primeRecoveryDelay has passed. Any read failure
// answers false, which keeps the existing fail-fast path.
func (e *Engine) primeAwaitsCapacity(ctx context.Context, deploymentID string, cause error) bool {
	if deploymentID == "" || customerCapacityProblem(cause) == nil {
		return false
	}
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil || dep.Status != state.DeploySnapshotting || len(dep.StageState) == 0 {
		return false
	}
	var stage state.StageState
	if err := json.Unmarshal(dep.StageState, &stage); err != nil {
		return false
	}
	if stage.Current != state.StageSnapshotPrepare || stage.CurrentStartedAt == nil {
		return false
	}
	return e.clockNow().Sub(*stage.CurrentStartedAt) < api.SnapshotPrimeCapacityMaxWait
}
