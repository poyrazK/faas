// adr: 20261017201014
package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/state"
)

type suspendResumeVMM interface {
	RehearseSuspendResume(context.Context, string, string) error
}
type routedSuspendResumeVMM interface {
	RehearseSuspendResumeOnNode(context.Context, string, string, string) error
}

func (c *VMMClient) RehearseSuspendResume(ctx context.Context, instance, receipt string) error {
	r, err := c.cli.RehearseSuspendResume(ctx, &vmmdpb.RehearseSuspendResumeRequest{Instance: instance, ReceiptId: receipt})
	if err != nil {
		return err
	}
	if r.GetInstance() != instance || r.GetReceiptId() != receipt {
		return errors.New("suspend/resume RPC did not acknowledge the exact lifecycle receipt")
	}
	return nil
}
func (r *VMMRouter) RehearseSuspendResumeOnNode(ctx context.Context, node, instance, receipt string) error {
	c, err := r.resolveFor(ctx, node)
	if err != nil {
		return err
	}
	v, ok := c.(suspendResumeVMM)
	if !ok {
		return errors.New("suspend/resume VM capability unavailable")
	}
	return v.RehearseSuspendResume(ctx, instance, receipt)
}

// RunScenarioSuspendResume never repeats an uncertain pause. The full ledger
// reservation is retained while the bounded operation holds the app lock.
func (e *Engine) RunScenarioSuspendResume(ctx context.Context, id string) (err error) {
	store, ok := e.store.(rehearsalresume.Store)
	if !ok {
		return errors.New("suspend/resume storage unavailable")
	}
	r, err := store.ScenarioSuspendResume(ctx, id)
	if err != nil {
		return err
	}
	release := e.lockApp(r.AppID)
	defer release()
	r, err = store.ScenarioSuspendResume(ctx, id)
	if err != nil {
		return err
	}
	if r.State == "complete" {
		return nil
	}
	ins, err := e.store.InstanceByID(ctx, r.InstanceID)
	if err != nil {
		return err
	}
	if ins.AppID != r.AppID || ins.DeploymentID != r.DeploymentID {
		return state.ErrConflict
	}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			_ = store.FailScenarioSuspendResume(cleanup, id)
			if current, readErr := e.store.InstanceByID(cleanup, ins.ID); readErr == nil && current.State == string(state.StateFrozen) {
				e.parkFrozenLocked(cleanup, current, "rehearsal_suspend_failed")
			}
		}
	}()
	if r.State != "armed" && r.State != "resumed" {
		return errors.New("suspend/resume original operation was lost; refusing to repeat it")
	}
	app, err := e.store.AppByID(ctx, r.AppID)
	if err != nil {
		return err
	}
	dep, err := e.store.DeploymentByID(ctx, r.DeploymentID)
	if err != nil {
		return err
	}
	captured, err := e.store.InstanceRuntimeConfigFence(ctx, r.AccountID, r.AppID, ins.ID)
	if err != nil {
		return fmt.Errorf("suspend/resume captured runtime: %w", err)
	}
	values, err := e.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		return err
	}
	if captured != values.ConfigFence {
		return errors.New("suspend/resume runtime configuration changed")
	}
	if r.State == "armed" {
		if ins.State != string(state.StateRunning) {
			return state.ErrConflict
		}
		if _, err = store.ClaimScenarioSuspendResume(ctx, id); err != nil {
			return err
		}
		v, capable := e.vmm.(routedSuspendResumeVMM)
		if !capable {
			return errors.New("suspend/resume VM capability unavailable")
		}
		if err = updateInstanceStateCAS(ctx, e.store, ins.ID, string(state.StateRunning), string(state.StateFrozen)); err != nil {
			return err
		}
		e.recordCommittedInstanceTransition(ctx, ins, state.StateRunning, state.StateFrozen, app.ID, "rehearsal_suspend", id)
		operation, cancel := context.WithTimeout(ctx, 155*time.Second)
		rpcErr := v.RehearseSuspendResumeOnNode(operation, ins.NodeID, ins.ID, id)
		cancel()
		// Proof is independent of a transport reply, including a successful reply.
		r, err = store.ScenarioSuspendResume(ctx, id)
		if err != nil || r.State != "resumed" {
			return errors.Join(rpcErr, err, errors.New("original suspend/resume lifecycle receipt is missing"))
		}
	}
	// Recheck configuration after the pause before routing any customer request.
	values, err = e.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		return err
	}
	if captured != values.ConfigFence {
		return errors.New("suspend/resume runtime configuration changed during pause")
	}
	current, err := e.store.InstanceByID(ctx, ins.ID)
	if err != nil {
		return err
	}
	if current.State == string(state.StateFrozen) {
		fresh, publishErr := e.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{AccountID: r.AccountID, AppID: r.AppID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID, ExpectedState: string(state.StateFrozen), Fence: captured.SecretFence, ConfigFence: captured, Netns: ins.Netns, HostIP: ins.HostIP, GuestUID: ins.GuestUID})
		if publishErr != nil {
			return publishErr
		}
		e.recordCommittedInstanceTransition(ctx, fresh, state.StateFrozen, state.StateRunning, app.ID, "rehearsal_resume", id)
	} else if current.State != string(state.StateRunning) {
		return state.ErrConflict
	}
	return store.CompleteScenarioSuspendResume(ctx, id)
}

func (l *Loop) dispatchScenarioSuspendResumes(ctx context.Context) {
	if l.engine == nil {
		return
	}
	store, ok := l.engine.store.(rehearsalresume.Store)
	if !ok {
		return
	}
	receipts, err := store.PendingScenarioSuspendResumes(ctx)
	if err != nil {
		l.log.Warn("sched: list rehearsal suspend/resumes", "err", err)
		return
	}
	for _, r := range receipts {
		l.workPool().submit(workRehearsalSuspend, r.ID, func() {
			if err := l.engine.RunScenarioSuspendResume(ctx, r.ID); err != nil {
				l.log.Warn("sched: rehearsal suspend/resume inconclusive", "receipt", r.ID, "err", err)
			}
		})
	}
}
