package toolregistry

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
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

// SmokeHostedTaskRehearsal is the operator harness entry for an authenticated
// owner. The in-process driver checks the dedicated host before a claim or
// build. Claim replay returns the run identity without exercising its VMs.
func (e *PrivateExecutor) SmokeHostedTaskRehearsal(ctx context.Context, account state.Account, intentID, ownerKeyID, planDigest string,
	driver tasktoolrehearsal.SmokeDriver) (state.HostedTaskRehearsalRun, tasktoolrehearsal.SmokeEvidence, error) {
	var evidence tasktoolrehearsal.SmokeEvidence
	if driver == nil {
		return state.HostedTaskRehearsalRun{}, evidence, state.ErrInvalidArgument
	}
	if err := driver.CheckHost(ctx, account.ID); err != nil {
		return state.HostedTaskRehearsalRun{}, evidence, fmt.Errorf("check the dedicated rehearsal host: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return state.HostedTaskRehearsalRun{}, evidence, err
	}
	run, execution, err := e.RunHostedTaskRehearsal(ctx, account, intentID, ownerKeyID, planDigest)
	if err != nil || execution == (taskrehearsal.Execution{}) {
		return run, evidence, err
	}
	evidence, err = e.opts.TaskRehearsalRunner.Smoke(ctx, e.opts.TaskRehearsalProvisioner, account, run, execution, driver)
	return run, evidence, err
}
