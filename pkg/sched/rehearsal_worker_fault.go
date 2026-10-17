// adr: 20261017201012
package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type workerFaultVMM interface {
	TerminateRehearsalWorker(context.Context, string, string) error
}
type routedWorkerFaultVMM interface {
	TerminateRehearsalWorkerOnNode(context.Context, string, string, string) error
}

func (c *VMMClient) TerminateRehearsalWorker(ctx context.Context, instance, fault string) error {
	r, err := c.cli.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: instance, RehearsalWorkerFaultId: fault})
	if err != nil {
		return err
	}
	if r.GetInstance() != instance || r.GetRehearsalWorkerFaultId() != fault {
		return errors.New("worker termination RPC did not acknowledge fault identity")
	}
	return nil
}
func (r *VMMRouter) TerminateRehearsalWorkerOnNode(ctx context.Context, node, instance, fault string) error {
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return err
	}
	vmm, ok := cli.(workerFaultVMM)
	if !ok {
		return errors.New("worker termination VM capability unavailable")
	}
	return vmm.TerminateRehearsalWorker(ctx, instance, fault)
}

// RunScenarioWorkerFault never retries an uncertain kill. A durable completed
// receipt can finish a scheduler transition after a lost RPC reply or restart.
func (e *Engine) RunScenarioWorkerFault(ctx context.Context, id string) error {
	store, ok := e.store.(rehearsalwork.FaultStore)
	if !ok {
		return errors.New("worker fault store unavailable")
	}
	f, err := store.ScenarioWorkerFault(ctx, id)
	if err != nil {
		return err
	}
	release := e.lockApp(f.AppID)
	defer release()
	f, err = store.ScenarioWorkerFault(ctx, id)
	if err != nil {
		return err
	}
	if f.State != "terminated" {
		f, err = store.ClaimScenarioWorkerFault(ctx, id)
		if err != nil {
			return err
		}
	}
	ins, err := e.store.InstanceByID(ctx, f.InstanceID)
	if err != nil {
		return err
	}
	if ins.AppID != f.AppID || ins.DeploymentID != f.DeploymentID {
		return state.ErrConflict
	}
	if f.State != "terminated" {
		vmm, supported := e.vmm.(routedWorkerFaultVMM)
		if !supported || !simulationFaultInstanceLive(ins.State) {
			_ = store.FailScenarioWorkerFault(ctx, id)
			return errors.New("worker termination owner unavailable")
		}
		operation, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = vmm.TerminateRehearsalWorkerOnNode(operation, ins.NodeID, ins.ID, id)
		cancel()
		// Receipt storage is independent of the RPC reply. Never infer cleanup from
		// a transport error or make a second destructive call.
		receipt, readErr := store.ScenarioWorkerFault(ctx, id)
		if readErr != nil || receipt.State != "terminated" {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			_ = store.FailScenarioWorkerFault(cleanup, id)
			return errors.Join(err, readErr, errors.New("worker termination receipt is missing"))
		}
	}
	if !simulationFaultInstanceLive(ins.State) {
		return nil
	}
	transitioned, err := e.transitionWithKindCAS(ctx, ins.ID, ins.AppID, state.StateStopped, "rehearsal_worker_termination", id)
	if err != nil {
		return fmt.Errorf("retire terminated rehearsal worker: %w", err)
	}
	if transitioned {
		e.ledger.Release(ins.ID)
	}
	return nil
}
func simulationFaultInstanceLive(s string) bool {
	return s == string(state.StateRunning) || s == string(state.StateColdBooting) || s == string(state.StateWaking) || s == string(state.StateDraining)
}

func (l *Loop) dispatchScenarioWorkerFaults(ctx context.Context) {
	if l.engine == nil {
		return
	}
	store, ok := l.engine.store.(rehearsalwork.FaultStore)
	if !ok {
		return
	}
	faults, err := store.PendingScenarioWorkerFaults(ctx)
	if err != nil {
		l.log.Warn("sched: list rehearsal worker faults", "err", err)
		return
	}
	for _, f := range faults {
		l.workPool().submit(workRehearsalFault, f.ID, func() {
			if err := l.engine.RunScenarioWorkerFault(ctx, f.ID); err != nil {
				l.log.Warn("sched: rehearsal worker fault remains inconclusive", "fault", f.ID, "err", err)
			}
		})
	}
}
