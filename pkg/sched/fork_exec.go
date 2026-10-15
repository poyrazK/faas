package sched

// ADR-732 fork exec, scheduler side. The fork coordinator that holds a
// fork's lease claims its queued commands one at a time and runs each
// through the vmmd of the fork's node, inside the quarantined fork.

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// ForkExecRuntime runs one fork exec. Engine implements it.
type ForkExecRuntime interface {
	ExecForkCommand(ctx context.Context, exec state.AppForkExec) (apptaskproto.Result, error)
}

// forkCommandVMM is the routed VMM capability behind fork exec.
type forkCommandVMM interface {
	ExecForkCommand(ctx context.Context, nodeID, instance string, req apptaskproto.Request) (apptaskproto.Result, error)
}

// ErrForkExecUnavailable means the fork has no running instance or its node's
// vmmd cannot run fork commands.
var ErrForkExecUnavailable = errors.New("sched: fork exec unavailable")

// ExecForkCommand runs exec inside its fork's running instance.
func (e *Engine) ExecForkCommand(ctx context.Context, exec state.AppForkExec) (apptaskproto.Result, error) {
	fork, err := e.store.AppForkForApp(ctx, exec.AppID, exec.ForkID)
	if err != nil || fork.Status != state.AppForkRunning || fork.InstanceID == nil {
		return apptaskproto.Result{}, ErrForkExecUnavailable
	}
	ins, err := e.store.InstanceByID(ctx, *fork.InstanceID)
	if err != nil || !state.IsFork(ins.Mode) || ins.NodeID == "" {
		return apptaskproto.Result{}, ErrForkExecUnavailable
	}
	runner, ok := e.vmm.(forkCommandVMM)
	if !ok {
		return apptaskproto.Result{}, ErrForkExecUnavailable
	}
	return runner.ExecForkCommand(ctx, ins.NodeID, ins.ID, apptaskproto.Request{
		Version: apptaskproto.Version, TaskID: exec.ID, Command: exec.Command, CommandShell: exec.CommandShell,
		TimeoutSeconds: exec.TimeoutSeconds, MaxOutputBytes: exec.MaxOutputBytes,
	})
}

// ExecForkCommand implements fork exec on one node's vmmd client.
func (c *VMMClient) ExecForkCommand(ctx context.Context, instance string, req apptaskproto.Request) (apptaskproto.Result, error) {
	var zero apptaskproto.Result
	if c == nil || c.cli == nil {
		return zero, errors.New("sched: nil vmmd client")
	}
	fields, _ := wire.FromContext(ctx)
	ctx = wire.WithCorrelationOutgoing(ctx, fields)
	resp, err := c.cli.ExecForkCommand(ctx, appTaskRequestToProto(instance, req))
	if err != nil {
		return zero, liftErr(err)
	}
	result := appTaskResultFromResponse(resp)
	if err := result.Validate(req.MaxOutputBytes); err != nil {
		return zero, err
	}
	return result, nil
}

// ExecForkCommand routes fork exec to the fork node's vmmd.
func (r *VMMRouter) ExecForkCommand(ctx context.Context, nodeID, instance string, req apptaskproto.Request) (apptaskproto.Result, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return apptaskproto.Result{}, err
	}
	runner, ok := cli.(interface {
		ExecForkCommand(context.Context, string, apptaskproto.Request) (apptaskproto.Result, error)
	})
	if !ok {
		return apptaskproto.Result{}, fmt.Errorf("%w: vmmd client does not run fork commands", ErrForkExecUnavailable)
	}
	return runner.ExecForkCommand(ctx, instance, req)
}

// forkExecFinish maps a runtime outcome onto the row's terminal result.
func forkExecFinish(exec state.AppForkExec, result apptaskproto.Result, err error) state.FinishAppForkExecParams {
	p := state.FinishAppForkExecParams{ID: exec.ID}
	if err != nil {
		p.Status, p.FailureCode, p.FailureMessage = state.AppForkExecFailed, "exec_failed", "the command could not be run in the fork"
		if errors.Is(err, ErrForkExecUnavailable) {
			p.FailureCode, p.FailureMessage = "fork_unavailable", "the fork is not running or cannot run commands"
		}
		return p
	}
	p.Status = state.AppForkExecStatus(result.Status)
	p.ExitCode, p.OutputTruncated = result.ExitCode, result.OutputTruncated
	p.Stdout, p.Stderr = result.Stdout, result.Stderr
	if result.FailureCode != "" {
		p.FailureCode, p.FailureMessage = truncateFailure(result.FailureCode, 64), truncateFailure(result.FailureMessage, 512)
		if p.FailureMessage == "" {
			p.FailureMessage = p.FailureCode
		}
	}
	return p
}

func truncateFailure(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
