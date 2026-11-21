package taskrehearsal

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

// Smoke exercises the exact contained workloads returned by a successful
// Run. Its owner caller must hold the original execution handoff.
func (r *Runner) Smoke(ctx context.Context, p *Provisioner, account state.Account, run state.HostedTaskRehearsalRun,
	execution Execution, driver tasktoolrehearsal.SmokeDriver) (tasktoolrehearsal.SmokeEvidence, error) {
	var evidence tasktoolrehearsal.SmokeEvidence
	if p == nil || r.Store == nil || driver == nil {
		return evidence, state.ErrInvalidArgument
	}
	target, err := p.target(account, run.ID)
	if err != nil {
		return evidence, err
	}
	scope := tasktoolrehearsal.SmokeScope{
		AccountID: account.ID, RunID: run.ID, PlanSHA256: run.PlanSHA256, Namespace: target.Namespace(),
		WebAppID: target.AppID(Web), WorkerAppID: target.AppID(Worker), ObserverAppID: target.AppID(Observer),
		WebDeploymentID: execution.WebDeploymentID, WorkerDeploymentID: execution.WorkerDeploymentID,
	}
	execute, stop := context.WithDeadline(ctx, run.ReservedAt.Add(leaseDuration))
	err = r.checkSmokeDeployments(execute, scope)
	if err == nil {
		evidence, err = tasktoolrehearsal.RunSmoke(execute, scope, driver)
	}
	stop()

	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	cleanupErr := p.Cleanup(cleanup, account.ID, run.ID)
	if cleanupErr != nil {
		return evidence, errors.Join(err, fmt.Errorf("dispose rehearsal workloads: %w", cleanupErr))
	}
	if verifyErr := driver.VerifyCleanup(cleanup, scope); verifyErr != nil {
		return evidence, errors.Join(err, fmt.Errorf("verify rehearsal cleanup: %w", verifyErr))
	}
	evidence.CleanupConfirmed = true
	return evidence, err
}

func (r *Runner) checkSmokeDeployments(ctx context.Context, scope tasktoolrehearsal.SmokeScope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, pair := range []struct{ app, deployment string }{
		{scope.WebAppID, scope.WebDeploymentID}, {scope.WorkerAppID, scope.WorkerDeploymentID},
	} {
		id, _ := apidsource.OperationIDs(buildOperation(scope.RunID), pair.app)
		if id != pair.deployment {
			return state.ErrConflict
		}
		deployment, err := r.Store.DeploymentByID(ctx, id)
		if err != nil {
			return fmt.Errorf("inspect rehearsal deployment: %w", err)
		}
		if deployment.ID != id || deployment.AppID != pair.app || deployment.Scope != api.DefaultEnvScope ||
			!deployment.TrafficPercentExplicit || deployment.TrafficPercent != 0 || deployment.RootfsKey == "" ||
			deployment.Status.IsTerminal() {
			return state.ErrConflict
		}
	}
	return ctx.Err()
}
