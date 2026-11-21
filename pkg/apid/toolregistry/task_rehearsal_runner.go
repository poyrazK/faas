package toolregistry

import (
	"context"

	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/state"
)

// RunHostedTaskRehearsal is the private execution entry for an authenticated
// owner. Replays return the original reservation and never run a build again.
func (e *PrivateExecutor) RunHostedTaskRehearsal(ctx context.Context, account state.Account, intentID, ownerKeyID, planDigest string) (state.HostedTaskRehearsalRun, taskrehearsal.Execution, error) {
	provisioner, runner := e.opts.TaskRehearsalProvisioner, e.opts.TaskRehearsalRunner
	if provisioner == nil || !provisioner.AdmissionEnabled || runner == nil || !runner.AdmissionEnabled {
		return state.HostedTaskRehearsalRun{}, taskrehearsal.Execution{}, state.ErrConflict
	}
	candidate, err := e.claimHostedTaskRehearsalCandidate(ctx, account.ID, intentID, ownerKeyID, planDigest, true)
	if err != nil || !candidate.hasSource() {
		return candidate.Run, taskrehearsal.Execution{}, err
	}
	defer clear(candidate.WebArchive)
	defer clear(candidate.WorkerArchive)
	execution, err := runner.Run(ctx, provisioner, account, candidate.Run, candidate.WebArchive, candidate.WorkerArchive, candidate.bindings)
	return candidate.Run, execution, err
}
