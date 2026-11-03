package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// The credential-free capture worker (ADR-20261031193635 slice 5a). It boots
// the parent deployment with prepareCredentialFreeCaptureBoot on a quarantined
// fork-mode instance, takes a warm capture into a capture key and always
// retires the VM. A fork-mode row is never routed, parked or snapshotted
// (ADR-732), and the quarantine network drops every guest-originated flow, so
// the capture boot can neither serve traffic nor reach anything. The capture
// is published only to environment_branch_captures, never to snapshots.

// errCaptureDeploymentGone fails a capture for good: the parent deployment
// can no longer run.
var errCaptureDeploymentGone = errors.New("sched: credential-free capture: parent deployment unavailable")

// CaptureEnvironmentBranch runs one leased capture attempt and returns the
// artifact to publish. The capture VM is retired before it returns.
func (e *Engine) CaptureEnvironmentBranch(ctx context.Context, capture state.EnvironmentBranchCapture) (state.EnvironmentBranchCaptureArtifact, error) {
	var zero state.EnvironmentBranchCaptureArtifact
	store, ok := e.store.(state.EnvironmentBranchCaptureStore)
	if !ok {
		return zero, fmt.Errorf("sched: credential-free capture store unavailable: %w", state.ErrConflict)
	}
	dep, err := e.store.DeploymentByID(ctx, capture.DeploymentID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return zero, errCaptureDeploymentGone
		}
		return zero, err
	}
	if securityQuarantineErr(dep) != nil {
		return zero, errCaptureDeploymentGone
	}
	ctx = WithScope(ctx, dep.Scope)
	release := e.lockApp(dep.AppID)
	defer release()
	if err := e.retireEnvironmentBranchCaptureInstance(ctx, capture.InstanceID); err != nil {
		return zero, err
	}
	app, acct, limits, err := e.resolveAppForDeploy(ctx, dep.AppID)
	if err != nil {
		return zero, err
	}
	if app.Status == state.AppDeleted {
		return zero, errCaptureDeploymentGone
	}
	if !acct.Active() {
		return zero, fmt.Errorf("sched: credential-free capture: account is not active: %w", state.ErrConflict)
	}
	if app, err = state.ResolveAppForDeployment(ctx, e.store, app, dep); err != nil {
		return zero, fmt.Errorf("sched: credential-free capture: workload settings: %w", err)
	}
	cpu := effectiveAppCPUMillicores(app)
	placement, err := e.choosePlacementLocked(ctx, Request{AppID: app.ID, Plan: acct.Plan, RAMMB: app.RAMMB,
		VCPU: limits.VCPU, CPUMillicores: cpu, MaxConcurrency: app.MaxConcurrency, PreferredNodeID: app.NodeID})
	if err != nil {
		return zero, err
	}
	ins, err := e.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateColdBooting),
		app.RAMMB, placement.NodeID, uuid.NewString(), string(state.InstanceModeFork))
	if err != nil {
		return zero, fmt.Errorf("sched: credential-free capture: instance row: %w", err)
	}
	if err := store.RecordEnvironmentBranchCaptureInstance(ctx, capture.ID, capture.LeaseToken, ins.ID); err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, app.ID, "branch_capture_lease_lost")
		return zero, fmt.Errorf("sched: credential-free capture: record instance: %w", err)
	}
	// The VM counts toward the node's RAM ceiling but never as serving
	// concurrency, exactly like a production fork.
	if err := e.ledger.Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, DeploymentScope: dep.Scope,
		Plan: acct.Plan, RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: cpu, MaxConcurrency: app.MaxConcurrency,
		Kind: KindFork, NodeID: placement.NodeID, NodeCeilingMB: placement.CeilingMB, VCPUBudget: placement.VCPUBudget,
		CPUBudgetMillicores: placement.CPUBudgetMillicores}); err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, app.ID, "branch_capture_admission")
		return zero, err
	}
	booted := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cancel()
		if booted {
			if err := e.timedDestroy(cleanupCtx, placement.NodeID, ins.ID, DestroyTimeout); err != nil {
				// The row stays live so the next claim retires the VM.
				e.log.Warn("sched: credential-free capture: destroy capture VM", "instance", ins.ID, "err", err)
				return
			}
		}
		e.ledger.Release(ins.ID)
		e.transitionWithKind(cleanupCtx, ins.ID, app.ID, state.StateStopped, "branch_capture_retired", "adr_20261031193635")
	}()
	prepared, err := e.prepareCredentialFreeCaptureBoot(ctx, app, acct, limits, dep, placement, ins)
	if err != nil {
		return zero, err
	}
	prepared.Spec.Quarantine = true
	if err := e.verifyPrimeLayer(ctx, app.ID, prepared.Spec.LayerKey); err != nil {
		return zero, err
	}
	// vmmd's readiness event takes its identity from this envelope.
	fields, _ := wire.FromContext(ctx)
	fields.AppID, fields.InstanceID, fields.WakeID, fields.NodeID = app.ID, ins.ID, ins.WakeID, placement.NodeID
	ctx = wire.WithContext(ctx, fields)
	bootCtx, cancel := context.WithTimeout(ctx, e.primeColdBootBudget(prepared.Spec.StartupDeadlineS))
	booted = true
	// Like a fork, the capture boot bypasses the rehearsal and managed
	// native admission wrappers: it must receive nothing but the
	// credential-free spec.
	out, err := e.vmm.CreateColdBoot(bootCtx, placement.NodeID, ins.ID, prepared.Spec)
	cancel()
	if err != nil {
		return zero, fmt.Errorf("sched: credential-free capture: cold boot: %w", err)
	}
	if out == nil || out.Instance != ins.ID {
		return zero, fmt.Errorf("sched: credential-free capture: cold boot response names another instance: %w", state.ErrConflict)
	}
	memKey := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierWarm, uuid.NewString())
	vmstateKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})
	captureCtx, cancel := context.WithTimeout(ctx, SnapshotBudgetFor(app.RAMMB))
	bytes, err := e.vmm.WarmSnapshot(captureCtx, placement.NodeID, ins.ID, memKey, vmstateKey)
	cancel()
	if err != nil {
		return zero, fmt.Errorf("sched: credential-free capture: snapshot: %w", err)
	}
	template, model := e.nodeCPUIdentity(ctx, placement.NodeID)
	return state.EnvironmentBranchCaptureArtifact{
		FCVersion: e.fcVer, StorageKey: memKey, BaseImageVersion: fcvm.FAAS_BASE_IMAGE_VERSION,
		CPUTemplate: template, CPUModel: model,
		MemBytes: bytes.MemBytes, DiskBytes: bytes.VMStateBytes, StoredBytes: bytes.StoredBytes,
	}, nil
}

// retireEnvironmentBranchCaptureInstance destroys a previous attempt's capture
// VM that schedd stopped before retiring. The caller holds the app lock.
func (e *Engine) retireEnvironmentBranchCaptureInstance(ctx context.Context, instanceID string) error {
	if instanceID == "" {
		return nil
	}
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sched: credential-free capture: load previous capture VM: %w", err)
	}
	switch state.State(ins.State) {
	case state.StateStopped, state.StateFailed:
		return nil
	}
	if !state.IsFork(ins.Mode) {
		return fmt.Errorf("sched: credential-free capture: previous capture VM %s is not quarantined: %w", ins.ID, state.ErrConflict)
	}
	return e.destroyFork(ctx, ins)
}

// EnvironmentBranchCaptureRuntime is the engine surface the worker drives.
type EnvironmentBranchCaptureRuntime interface {
	CaptureEnvironmentBranch(ctx context.Context, capture state.EnvironmentBranchCapture) (state.EnvironmentBranchCaptureArtifact, error)
}

// EnvironmentBranchCaptureWorker claims requested captures and runs them one
// at a time: a capture is opportunistic work that must not crowd out wakes.
type EnvironmentBranchCaptureWorker struct {
	store   state.EnvironmentBranchCaptureStore
	runtime EnvironmentBranchCaptureRuntime
	poll    time.Duration
	log     *slog.Logger
	now     func() time.Time
}

// NewEnvironmentBranchCaptureWorker builds a worker polling every poll.
func NewEnvironmentBranchCaptureWorker(store state.EnvironmentBranchCaptureStore, runtime EnvironmentBranchCaptureRuntime, poll time.Duration, log *slog.Logger) *EnvironmentBranchCaptureWorker {
	if poll <= 0 {
		poll = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &EnvironmentBranchCaptureWorker{store: store, runtime: runtime, poll: poll, log: log, now: time.Now}
}

// Run ticks until ctx ends.
func (w *EnvironmentBranchCaptureWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick runs every due capture.
func (w *EnvironmentBranchCaptureWorker) Tick(ctx context.Context) {
	for ctx.Err() == nil {
		lease := uuid.NewString()
		capture, err := w.store.ClaimEnvironmentBranchCapture(ctx, lease, w.now().UTC(), api.EnvironmentBranchCaptureLease)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			w.log.Warn("environment branch capture: claim", "err", err)
			return
		}
		if !w.capture(ctx, capture) {
			// Retry on a later tick rather than in a hot loop.
			return
		}
	}
}

// capture runs one attempt and reports whether it published.
func (w *EnvironmentBranchCaptureWorker) capture(ctx context.Context, capture state.EnvironmentBranchCapture) bool {
	// Finish inside the lease so a later claim never races a live attempt.
	attemptCtx, cancel := context.WithTimeout(ctx, api.EnvironmentBranchCaptureLease*9/10)
	artifact, err := w.runtime.CaptureEnvironmentBranch(attemptCtx, capture)
	cancel()
	if err == nil {
		err = w.store.PublishEnvironmentBranchCapture(ctx, capture.ID, capture.LeaseToken, artifact)
		if err == nil {
			w.log.Info("environment branch capture: ready", "deployment_id", capture.DeploymentID, "attempts", capture.Attempts)
			return true
		}
	}
	permanent := errors.Is(err, errCredentialFreeCaptureUnsupported) || errors.Is(err, errCaptureDeploymentGone) ||
		capture.Attempts >= api.EnvironmentBranchCaptureMaxAttempts
	w.log.Warn("environment branch capture: attempt failed", "deployment_id", capture.DeploymentID,
		"attempts", capture.Attempts, "permanent", permanent, "err", err)
	if ferr := w.store.FailEnvironmentBranchCapture(ctx, capture.ID, capture.LeaseToken, err.Error(), permanent); ferr != nil {
		w.log.Warn("environment branch capture: record failure", "deployment_id", capture.DeploymentID, "err", ferr)
	}
	return false
}
