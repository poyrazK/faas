package toolregistry

import (
	"context"

	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/state"
)

// ProvisionHostedTaskRehearsal is the private control-plane launch entry. Only
// the call that wins the native claim can create apps or allocate a database.
// Replays return the original reservation with no archives or prepared target.
func (e *PrivateExecutor) ProvisionHostedTaskRehearsal(ctx context.Context, account state.Account, intentID, ownerKeyID, planDigest string) (HostedTaskRehearsalCandidate, *taskrehearsal.Prepared, error) {
	provisioner := e.opts.TaskRehearsalProvisioner
	if provisioner == nil || !provisioner.AdmissionEnabled {
		return HostedTaskRehearsalCandidate{}, nil, state.ErrConflict
	}
	candidate, err := e.ClaimHostedTaskRehearsalCandidate(ctx, account.ID, intentID, ownerKeyID, planDigest)
	if err != nil || !candidate.hasSource() {
		return candidate, nil, err
	}
	target, err := provisioner.Provision(ctx, account, candidate.Run)
	if err != nil {
		return HostedTaskRehearsalCandidate{Run: candidate.Run}, nil, err
	}
	return candidate, target, nil
}

func (c HostedTaskRehearsalCandidate) hasSource() bool {
	return len(c.WebArchive) != 0 && len(c.WorkerArchive) != 0
}

// CleanupHostedTaskRehearsal is the private recovery entry for an inactive run.
// Admission flags do not prevent disposal of durable resources.
func (e *PrivateExecutor) CleanupHostedTaskRehearsal(ctx context.Context, accountID, runID string) error {
	provisioner := e.opts.TaskRehearsalProvisioner
	if provisioner == nil {
		return state.ErrConflict
	}
	if err := provisioner.Cleanup(ctx, accountID, runID); err != nil {
		return err
	}
	if e.opts.TaskRehearsalRunner != nil {
		return e.opts.TaskRehearsalRunner.RemoveRunSources(ctx, runID)
	}
	return nil
}
