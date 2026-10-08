package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Fork restore refusals (ADR-732). Each maps to a stable app_forks
// failure_code through ForkFailureCode.
var (
	ErrForkDeploymentUnavailable = errors.New("sched: fork deployment unavailable")
	ErrForkAccountInactive       = errors.New("sched: fork account is not active")
	ErrForkNoCapture             = errors.New("sched: fork has no usable capture")
)

// ForkRestore is what a successful restore produced.
type ForkRestore struct {
	InstanceID string
	SnapshotID string
	NodeID     string
}

// ForkFailureCode maps a restore error to the app_forks failure_code the
// customer sees. Admission refusals are capacity: forks lose to wakes and
// are refused, never queued.
func ForkFailureCode(err error) (code, message string) {
	switch {
	case errors.Is(err, ErrForkNoCapture):
		return "no_capture", "the deployment has no snapshot to fork yet; send it some traffic and try again"
	case errors.Is(err, ErrForkAccountInactive):
		return "account_inactive", "the account is suspended; resolve billing to create forks"
	case errors.Is(err, ErrForkDeploymentUnavailable):
		return "deployment_unavailable", "the forked deployment is no longer available"
	case errors.Is(err, ErrNoCapacity), errors.Is(err, ErrAtCapacity), isCapacityProblem(err):
		return "no_capacity", "there is no room for the fork on this node right now; try again later"
	default:
		return "restore_failed", "the fork could not be restored"
	}
}

// isCapacityProblem reports the ledger's RAM/vCPU refusal (api.ErrCapacity).
func isCapacityProblem(err error) bool {
	var problem *api.Problem
	return errors.As(err, &problem) && problem.Code == api.CodeCapacity
}

// RestoreFork restores the newest compatible capture of the fork's pinned
// deployment into a new quarantined instance with mode='fork' (ADR-732).
//
// It deliberately does not go through admitAndDispatch: a fork is not a
// wake, so it must not trigger routing, rollout grants, scale-out stamps
// or pressure parking. It reserves RAM as KindFork (never a serving slot),
// sends no sealed secrets, and asks vmmd for the quarantine network.
func (e *Engine) RestoreFork(ctx context.Context, fork state.AppFork) (ForkRestore, error) {
	release := e.lockApp(fork.AppID)
	defer release()

	app, dep, acct, err := e.resolveForkTarget(ctx, fork)
	if err != nil {
		return ForkRestore{}, err
	}
	snap, err := e.forkSnapshot(ctx, fork, dep, string(acct.Plan), app)
	if err != nil {
		return ForkRestore{}, err
	}
	choice := wakeSnapshotChoice{snap: snap, ok: true}
	limits := api.MustLimitsFor(acct.Plan)
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID: app.ID, Plan: acct.Plan, RAMMB: app.RAMMB, VCPU: limits.VCPU,
		CPUMillicores: effectiveAppCPUMillicores(app), MaxConcurrency: app.MaxConcurrency,
		PreferredNodeID: app.NodeID,
	})
	if err != nil {
		return ForkRestore{}, err
	}
	ins, err := e.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking),
		app.RAMMB, placement.NodeID, uuid.NewString(), string(state.InstanceModeFork))
	if err != nil {
		return ForkRestore{}, fmt.Errorf("sched: fork instance row: %w", err)
	}
	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, DeploymentScope: dep.Scope, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app),
		MaxConcurrency: app.MaxConcurrency, Kind: KindFork, NodeID: placement.NodeID,
		NodeCeilingMB: placement.CeilingMB, VCPUBudget: placement.VCPUBudget,
		CPUBudgetMillicores: placement.CPUBudgetMillicores,
	}); err != nil {
		e.transitionWithKind(context.WithoutCancel(ctx), ins.ID, app.ID, state.StateFailed, "fork_restore_error", "admission")
		return ForkRestore{}, err
	}
	if err := e.bootFork(ctx, ins, app, dep, acct, placement, choice.snap); err != nil {
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.rollbackAdmittedInstance(ctx, ins.ID, app.ID, "fork_restore_failed")
		return ForkRestore{}, err
	}
	return ForkRestore{InstanceID: ins.ID, SnapshotID: choice.snap.ID, NodeID: placement.NodeID}, nil
}

// forkSnapshot picks what a fork restores: its ADR-733 crash capture when it
// is pinned to one (which must still be ready, unexpired and decrypted for
// the fork), otherwise the deployment's newest compatible snapshot.
func (e *Engine) forkSnapshot(ctx context.Context, fork state.AppFork, dep state.Deployment, plan string, app state.App) (state.Snapshot, error) {
	if fork.CrashCaptureID != nil {
		capture, err := e.store.CrashCaptureForRestore(ctx, *fork.CrashCaptureID)
		if err != nil || capture.AppID != fork.AppID || capture.DeploymentID != dep.ID ||
			capture.ExpiresAt == nil || !capture.ExpiresAt.After(time.Now()) || !capture.PlaintextReadable() {
			return state.Snapshot{}, ErrForkNoCapture
		}
		snap, ok := capture.Snapshot()
		if !ok || snap.FCVersion != e.fcVer {
			return state.Snapshot{}, ErrForkNoCapture
		}
		return snap, nil
	}
	choice := e.chooseWakeSnapshot(ctx, dep.ID, plan, app.RAMMB, app.AppProtocol)
	if !choice.ok || choice.snap.StorageKey == "" {
		return state.Snapshot{}, ErrForkNoCapture
	}
	return choice.snap, nil
}

// resolveForkTarget loads the fork's app, pinned deployment and account,
// refusing anything the fork may no longer run against.
func (e *Engine) resolveForkTarget(ctx context.Context, fork state.AppFork) (state.App, state.Deployment, state.Account, error) {
	app, err := e.store.AppByID(ctx, fork.AppID)
	if err != nil || app.AccountID != fork.AccountID || app.Status == state.AppDeleted {
		return state.App{}, state.Deployment{}, state.Account{}, ErrForkDeploymentUnavailable
	}
	dep, err := e.store.DeploymentByID(ctx, fork.DeploymentID)
	if err != nil || dep.AppID != app.ID || securityQuarantineErr(dep) != nil {
		return state.App{}, state.Deployment{}, state.Account{}, ErrForkDeploymentUnavailable
	}
	if app, err = state.ResolveAppForDeployment(ctx, e.store, app, dep); err != nil {
		return state.App{}, state.Deployment{}, state.Account{}, fmt.Errorf("sched: fork workload settings: %w", err)
	}
	acct, err := e.store.AccountByID(ctx, fork.AccountID)
	if err != nil || acct.ID != app.AccountID {
		return state.App{}, state.Deployment{}, state.Account{}, ErrForkDeploymentUnavailable
	}
	if !acct.Active() {
		return state.App{}, state.Deployment{}, state.Account{}, ErrForkAccountInactive
	}
	return app, dep, acct, nil
}

// bootFork restores the capture through vmmd with Quarantine set, then
// publishes the runtime identity and moves the row to RUNNING. No sealed
// secrets are staged; the capture's own memory is the fork's state.
func (e *Engine) bootFork(ctx context.Context, ins state.Instance, app state.App, dep state.Deployment,
	acct state.Account, placement Placement, snap state.Snapshot) error {
	limits := api.MustLimitsFor(acct.Plan)
	spec := AppSpec{
		BaseKey: baseKey(app.Runtime), LayerKey: layerKey(dep.RootfsKey, dep.ID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB), //nolint:gosec // plan-bounded
		CPUMillicores: int32(effectiveAppCPUMillicores(app)), EgressMbit: int32(limits.EgressMbit), //nolint:gosec // plan-bounded
		StartupDeadlineS: startupDeadlineForApp(app, acct.Plan), ExecutionMode: executionModeForApp(app),
		Plan: acct.Plan, AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID,
		Port: deploymentRuntimePort(dep), HealthcheckPath: healthcheckPathFromDep(dep),
		Runtime: app.Runtime, AppProtocol: app.AppProtocol,
		Quarantine: true,
	}
	if spec.BaseKey == "" || !spec.Plan.Valid() {
		return errors.New("sched: fork runtime projection is incomplete")
	}
	vmstatePath, vmstateKey := e.snapshotStateLocators(placement.NodeID, snap)
	out, err := e.vmm.CreateFromSnapshot(ctx, placement.NodeID, ins.ID, spec, SnapshotRef{
		DeploymentID: dep.ID, FCVersion: snap.FCVersion, StorageKey: snap.StorageKey,
		VMStatePath: vmstatePath, VMStateStorageKey: vmstateKey,
	})
	if err != nil {
		return fmt.Errorf("sched: fork restore: %w", err)
	}
	if err := e.store.SetInstanceRuntime(ctx, ins.ID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		return fmt.Errorf("sched: fork runtime identity: %w", err)
	}
	ok, err := e.transitionWithKindCAS(ctx, ins.ID, app.ID, state.StateRunning, "fork_restored", "adr_732")
	if err != nil {
		return fmt.Errorf("sched: fork publish: %w", err)
	}
	if !ok {
		return errors.New("sched: fork publish: instance changed during restore")
	}
	return nil
}

// DestroyForkInstance ends a fork's instance under the app lock. A row that
// is already terminal, or missing, is a no-op so teardown is idempotent.
func (e *Engine) DestroyForkInstance(ctx context.Context, appID, instanceID string) error {
	release := e.lockApp(appID)
	defer release()
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !state.IsFork(ins.Mode) {
		return fmt.Errorf("sched: destroy fork: instance %s is not a fork", instanceID)
	}
	if s := state.State(ins.State); s == state.StateStopped || s == state.StateFailed {
		e.ledger.Release(instanceID)
		return nil
	}
	return e.destroyFork(ctx, ins)
}
