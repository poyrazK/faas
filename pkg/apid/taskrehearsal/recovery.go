package taskrehearsal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

// NativeRecovery is a private operator harness for the same pinned startup
// fixture. The scheduler owns interruption of the observed worker.
type NativeRecovery struct {
	*NativeStartup
	Interrupt func(context.Context, tasktoolrehearsal.WorkerInterruption) (tasktoolrehearsal.StoppedWorker, error)
}

func (d *NativeRecovery) Preflight(ctx context.Context, accountID string) error {
	if d == nil || d.Interrupt == nil {
		return state.ErrInvalidArgument
	}
	return d.NativeStartup.Preflight(ctx, accountID)
}

// RunRecovery qualifies worker replacement and durable result recovery on a
// fresh owner-claimed run. The existing smoke coordinator verifies the proof.
func (r *Runner) RunRecovery(ctx context.Context, p *Provisioner, account state.Account, run state.HostedTaskRehearsalRun,
	web, worker []byte, policy tasktoolbinding.Policy, recovery *NativeRecovery) (tasktoolrehearsal.SmokeEvidence, error) {
	var evidence tasktoolrehearsal.SmokeEvidence
	if recovery == nil || recovery.NativeStartup == nil || recovery.Interrupt == nil {
		return evidence, state.ErrInvalidArgument
	}
	var observe pgruntime.TaskObservation
	execution, err := r.run(ctx, p, account, run, web, worker, policy, &observe)
	if err != nil {
		return evidence, err
	}
	target, err := p.target(account, run.ID)
	if err != nil {
		return evidence, err
	}
	target.expiresAt = run.ReservedAt.Add(leaseDuration)
	driver := &recoveryDriver{NativeRecovery: recovery, runner: r, target: target, observe: observe}
	defer func() {
		if driver.session.close != nil {
			driver.session.close()
		}
	}()
	return r.Smoke(ctx, p, account, run, execution, driver)
}

type recoveryDriver struct {
	*NativeRecovery
	runner        *Runner
	target        *Prepared
	observe       pgruntime.TaskObservation
	session       startupSession
	initial       observedClaim
	interruptedAt time.Time
}

func (d *recoveryDriver) CheckHost(ctx context.Context, accountID string) error {
	return d.Preflight(ctx, accountID)
}

func (d *recoveryDriver) SubmitReport(ctx context.Context, scope tasktoolrehearsal.SmokeScope) (string, error) {
	if d.observe.Claim == nil || d.observe.Result == nil {
		return "", state.ErrInvalidArgument
	}
	var err error
	d.session, err = d.runner.openStartupSession(ctx, d.target, scope, d.NativeStartup)
	if err != nil {
		return "", err
	}
	return submitReport(ctx, d.Forward(d.session.web, d.session.port))
}

func (d *recoveryDriver) WaitForClaim(ctx context.Context, scope tasktoolrehearsal.SmokeScope, taskID string, attempt int) (tasktoolrehearsal.WorkerClaim, error) {
	since := d.session.submittedAt
	if attempt == 2 {
		since = d.interruptedAt
	}
	claim, err := d.readClaim(ctx, scope, d.session.logs, d.observe.Claim, taskID, since, attempt)
	if err != nil {
		return tasktoolrehearsal.WorkerClaim{}, err
	}
	if attempt == 1 {
		d.initial = claim
	} else if claim.lease.WorkerID == d.initial.lease.WorkerID || claim.lease.LeaseSHA256 == d.initial.lease.LeaseSHA256 ||
		claim.instance.WakeID == d.initial.instance.WakeID {
		return tasktoolrehearsal.WorkerClaim{}, state.ErrConflict
	}
	return claim.worker, nil
}

func (d *recoveryDriver) StopWorker(ctx context.Context, scope tasktoolrehearsal.SmokeScope, claim tasktoolrehearsal.WorkerClaim) (tasktoolrehearsal.StoppedWorker, error) {
	var stopped tasktoolrehearsal.StoppedWorker
	if claim != d.initial.worker || claim.Attempt != 1 || !d.initial.checkpoint || !d.interruptedAt.IsZero() {
		return stopped, state.ErrConflict
	}
	ready, err := d.runner.startupDeploymentsReady(ctx, d.target, scope, d.Store)
	if err != nil || !ready {
		return stopped, errors.Join(state.ErrConflict, err)
	}
	observedAt := time.Now()
	if age := observedAt.Sub(d.initial.writtenAt); age < 0 || age >= tasktoolrehearsal.WorkerStopDeadline {
		return stopped, errors.New("rehearsal first-attempt checkpoint is stale")
	}
	live, err := d.observe.Claim(ctx, d.initial.lease)
	if err != nil || !live {
		return stopped, errors.Join(errors.New("initial worker lease changed before interruption"), err)
	}
	d.interruptedAt = observedAt
	ins := d.initial.instance
	return d.Interrupt(ctx, tasktoolrehearsal.WorkerInterruption{
		AccountID: scope.AccountID, RunID: scope.RunID, PlanSHA256: scope.PlanSHA256, TaskID: claim.TaskID,
		AppID: ins.AppID, DeploymentID: ins.DeploymentID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID, ObservedAt: observedAt,
	})
}

func (d *recoveryDriver) WaitForResult(ctx context.Context, scope tasktoolrehearsal.SmokeScope, taskID string) (tasktoolrehearsal.SmokeResult, error) {
	var result tasktoolrehearsal.SmokeResult
	ready, err := d.runner.startupDeploymentsReady(ctx, d.target, scope, d.Store)
	if err != nil || !ready {
		return result, errors.Join(state.ErrConflict, err)
	}
	web := d.session.web
	current, err := d.Store.InstanceByID(ctx, web.ID)
	if err != nil || !runningInstance(current, web.ID, scope.WebAppID, scope.WebDeploymentID, state.InstanceModeNormal) ||
		current.NodeID != web.NodeID || current.WakeID != web.WakeID {
		return result, errors.Join(state.ErrConflict, err)
	}
	client, err := openRehearsalMCP(ctx, d.Forward(web, d.session.port))
	if err != nil {
		return result, err
	}
	err = waitFor(ctx, func(ctx context.Context) (bool, error) {
		task, _, err := client.GetTask(ctx, taskID)
		if err != nil || task.TaskID != taskID {
			return false, errors.New("read recovered Task through the pinned MCP endpoint")
		}
		if task.Status == "working" {
			return false, nil
		}
		var report struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if task.Status != "completed" || json.Unmarshal(task.Result, &report) != nil || report.IsError ||
			len(report.Content) != 1 || report.Content[0].Type != "text" {
			return false, errors.New("recovered Task did not return a completed report")
		}
		result = tasktoolrehearsal.SmokeResult{TaskID: taskID, Status: task.Status, Text: report.Content[0].Text}
		return true, nil
	})
	return result, err
}

func (d *recoveryDriver) ObserveResult(ctx context.Context, _ tasktoolrehearsal.SmokeScope, taskID string) (tasktoolrehearsal.ObservedResult, error) {
	return d.observe.Result(ctx, taskID)
}

func (d *recoveryDriver) VerifyCleanup(ctx context.Context, _ tasktoolrehearsal.SmokeScope) error {
	return waitFor(ctx, func(ctx context.Context) (bool, error) {
		for _, role := range workloads {
			id := d.target.AppID(role.name)
			instances, err := state.ListLiveInstancesForApp(ctx, d.Store, id)
			if err != nil {
				return false, fmt.Errorf("inspect %s workload teardown: %w", role.name, err)
			}
			for _, instance := range instances {
				if state.IsLive(instance.State) {
					return false, nil
				}
			}
		}
		return true, nil
	})
}
