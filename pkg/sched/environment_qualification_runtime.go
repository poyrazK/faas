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

// WithEnvironmentWorkloadQualificationRuntime owns one bounded native execution
// window. The visitor must produce release/smoke/snapshot evidence separately;
// successful boot alone never emits deployment_ready or activates a graph.
// Always retire this VM before returning, including failed/stale callbacks.
func (e *Engine) WithEnvironmentWorkloadQualificationRuntime(ctx context.Context, claimed state.EnvironmentWorkloadQualificationRequest, visit func(context.Context, state.Instance) error) (result error) {
	qualifier, ok := e.store.(state.EnvironmentGitOpsQualificationStore)
	admitter, admissionOK := e.store.(state.EnvironmentGitOpsQualificationInstanceStore)
	publisher, publishOK := e.store.(state.EnvironmentGitOpsQualificationRuntimeStore)
	executor, executionOK := e.store.(state.EnvironmentQualificationExecutionStore)
	vm, nativeOK := e.vmm.(EnvironmentQualificationVMM)
	if !ok || !admissionOK || !publishOK || visit == nil || claimed.LeaseUntil == nil || claimed.ReservedInstanceID == "" {
		return state.ErrInvalidArgument
	}
	if !executionOK || !nativeOK {
		return fmt.Errorf("qualification requires attempt-aware VM execution and retirement: %w", state.ErrConflict)
	}
	ctx, deadlineCancel := context.WithDeadline(WithScope(ctx, claimed.FrozenInputs.Scope), *claimed.LeaseUntil)
	defer deadlineCancel()
	release, err := e.lockQualificationApp(ctx, claimed.AppID)
	if err != nil {
		return err
	}
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()
	if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	defer close(done)
	go e.watchQualificationOwner(ctx, qualifier, claimed, cancel, done)

	app, acct, limits, err := e.resolveAppForDeploy(ctx, claimed.AppID)
	if err != nil {
		return err
	}
	if !acct.MayDeploy() {
		return acct.DeployBlockedProblem()
	}
	dep, err := e.store.DeploymentByID(ctx, claimed.DeploymentID)
	if err != nil {
		return err
	}
	app, err = state.AppForDeploymentRuntime(app, dep)
	if err != nil || !dep.EnvironmentWorkloadHeld() {
		return errors.Join(state.ErrConflict, err)
	}
	if err := securityQuarantineErr(dep); err != nil {
		return err
	}
	cpu := effectiveAppCPUMillicores(app)
	startupCPU := cpu
	if !dep.DisableStartupCPUBoost {
		startupCPU = startupCPUBoostQuota(acct.Plan, cpu)
	}
	placement, err := e.choosePlacementLocked(ctx, Request{AppID: app.ID, Plan: acct.Plan, RAMMB: app.RAMMB,
		VCPU: limits.VCPU, CPUMillicores: startupCPU, MaxConcurrency: app.MaxConcurrency, PreferredNodeID: app.NodeID})
	if err != nil {
		return err
	}
	admission, err := admitter.CreateEnvironmentWorkloadQualificationInstance(ctx, claimed, state.EnvironmentWorkloadQualificationPlacement{
		NodeID: placement.NodeID, WakeID: uuid.NewString(), RAMMB: app.RAMMB})
	if err != nil {
		return err
	}
	if !admission.Created {
		// An uncertain earlier boot must be recovered/retired explicitly. A
		// second invocation cannot replay a VM RPC on the same reservation.
		return state.ErrConflict
	}
	ins, vmAttempted := admission.Instance, false
	frame := admission.Execution
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		proof := state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}
		if vmAttempted {
			evidence, err := vm.RetireEnvironmentQualification(cleanupCtx, frame)
			if err != nil || evidence.Execution != frame {
				// Preserve the active row and ledger reservation until physical
				// retirement is confirmed; expiry cannot start a parallel VM.
				result = errors.Join(result, fmt.Errorf("qualification VM retirement: %w", errors.Join(err, state.ErrConflict)))
				return
			}
			proof = evidence.Retirement
		}
		if !locked {
			cleanupRelease, err := e.lockQualificationApp(cleanupCtx, claimed.AppID)
			if err != nil {
				// Destruction alone is not durable retirement. Keep the row
				// and ledger charge until recovery can complete that write.
				result = errors.Join(result, fmt.Errorf("qualification retirement app lock: %w", err))
				return
			}
			defer cleanupRelease()
		}
		if err := executor.RetireEnvironmentQualificationExecution(cleanupCtx, frame, proof); err != nil {
			result = errors.Join(result, err)
			return
		}
		e.releaseHostPortLeases(cleanupCtx, frame.NodeID, frame.InstanceID)
		e.recordQualificationInstanceTransition(cleanupCtx, ins, state.State(ins.State), state.StateStopped, "environment_qualification_retired")
		e.ledger.Release(ins.ID)
	}()
	if startupCPU > cpu {
		if err := e.store.SetInstanceStartupCPUBoostUntil(ctx, ins.ID, claimed.LeaseUntil); err != nil {
			return fmt.Errorf("qualification: persist startup CPU reservation: %w", err)
		}
	}
	e.emitInstanceChanged(ctx, ins.ID, app.ID, state.StateColdBooting, ins.WakeID)
	// Count this candidate in the app's total, with only the single reviewed
	// replacement overlap when the original scope still has a live revision.
	serving, servingErr := e.store.LiveDeploymentForScope(ctx, app.ID, dep.Scope)
	if servingErr != nil && !errors.Is(servingErr, state.ErrNotFound) {
		return servingErr
	}
	if err := e.ledger.Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, DeploymentScope: dep.Scope,
		Plan: acct.Plan, RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: cpu,
		CPUStartupBoostMillicores: startupCPU, CPUStartupBoostUntil: *claimed.LeaseUntil, MaxConcurrency: app.MaxConcurrency,
		AllowConcurrencyOverlap: servingErr == nil && serving.ID != dep.ID, NodeID: placement.NodeID,
		NodeCeilingMB: placement.CeilingMB, VCPUBudget: placement.VCPUBudget, CPUBudgetMillicores: placement.CPUBudgetMillicores}); err != nil {
		return err
	}
	prepared, err := e.prepareDeploymentPrimeBoot(ctx, app, acct, limits, dep, placement, ins)
	if err != nil {
		return err
	}
	delivery := bootInput{insID: ins.ID, appID: app.ID, accountID: acct.ID, wakeID: ins.WakeID, secretDeliveries: prepared.SecretDeliveries}
	deliveryFinalized := false
	defer func() {
		if !deliveryFinalized {
			e.recordAppSecretDelivery(ctx, delivery, state.SecretDeliveryFailed, "runtime_start_failed")
		}
	}()
	if err := e.verifyPrimeLayer(ctx, app.ID, prepared.Spec.LayerKey); err != nil {
		return err
	}
	if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
		return err
	}
	// VM effects and evidence checks may consume the full lease window.
	// Existing serving wakes retain access to the app lock during that work;
	// the attempt and reserved instance are fenced at every publication.
	release()
	locked = false
	bootCtx, bootCancel := context.WithTimeout(ctx, e.budgetFor(state.StateColdBooting))
	defer bootCancel()
	if err := executor.MarkEnvironmentQualificationDispatched(bootCtx, claimed, frame); err != nil {
		return err
	}
	vmAttempted = true
	out, err := vm.CreateEnvironmentQualification(bootCtx, frame, prepared.Spec)
	if err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	if out == nil {
		return state.ErrConflict
	}
	if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
		return err
	}
	ins, err = publisher.PublishEnvironmentWorkloadQualificationRuntime(ctx, claimed, state.EnvironmentWorkloadQualificationRuntime{
		NodeID: ins.NodeID, WakeID: ins.WakeID, Netns: out.Netns, HostIP: out.HostIP, GuestUID: int(out.LeaseUID), Inputs: prepared.Inputs})
	if err != nil {
		// Keep the admitted identity for cleanup even when publication failed.
		ins = admission.Instance
		return err
	}
	e.recordQualificationInstanceTransition(ctx, ins, state.StateColdBooting, state.StateRunning, "environment_qualification")
	e.recordAppSecretDelivery(ctx, delivery, state.SecretDeliveryDelivered, "")
	deliveryFinalized = true
	if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
		return err
	}
	if err := visit(ctx, ins); err != nil {
		return err
	}
	return e.validateQualificationOwner(ctx, qualifier, claimed)
}

// A private attempt remains observable without asking the ordinary service or
// worker controller to change serving capacity before graph activation.
func (e *Engine) recordQualificationInstanceTransition(ctx context.Context, ins state.Instance, from, to state.State, reason string) {
	e.emitInstanceChanged(ctx, ins.ID, ins.AppID, to, ins.WakeID)
	e.appendInstanceTransitionEvent(ctx, ins, from, to, "state_transition", reason)
}

// Contention must respect the execution/cleanup deadline without leaving a
// waiter goroutine that could acquire the mutex after its caller has returned.
func (e *Engine) lockQualificationApp(ctx context.Context, appID string) (func(), error) {
	mu := e.appMutex(appID)
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, context.Cause(ctx))
		}
		if mu.TryLock() {
			if err := ctx.Err(); err != nil {
				mu.Unlock()
				return nil, errors.Join(err, context.Cause(ctx))
			}
			return mu.Unlock, nil
		}
		timer := time.NewTimer(api.EnvironmentGitOpsQualificationRuntimeCheckInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.Join(ctx.Err(), context.Cause(ctx))
		case <-timer.C:
		}
	}
}

func (e *Engine) validateQualificationOwner(ctx context.Context, qualifier state.EnvironmentGitOpsQualificationStore, claimed state.EnvironmentWorkloadQualificationRequest) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	app, err := e.store.AppByID(ctx, claimed.AppID)
	if err != nil {
		return err
	}
	if !e.ownsApp(app) {
		return state.ErrConflict
	}
	if err := qualifier.ValidateEnvironmentWorkloadQualification(ctx, claimed); err != nil {
		return err
	}
	ins, err := e.store.InstanceByID(ctx, claimed.ReservedInstanceID)
	if errors.Is(err, state.ErrNotFound) {
		return nil // Initial validation precedes admission.
	}
	if err != nil {
		return err
	}
	if ins.AppID != claimed.AppID || ins.DeploymentID != claimed.DeploymentID || ins.RAMMB != app.RAMMB ||
		(state.State(ins.State) != state.StateColdBooting && state.State(ins.State) != state.StateRunning) {
		return state.ErrConflict
	}
	node, err := e.store.ComputeNodeByID(ctx, ins.NodeID)
	if err != nil {
		return err
	}
	if !node.Active || node.Lifecycle != state.NodeLifecycleActive {
		return state.ErrConflict
	}
	return nil
}

func (e *Engine) watchQualificationOwner(ctx context.Context, qualifier state.EnvironmentGitOpsQualificationStore, claimed state.EnvironmentWorkloadQualificationRequest, cancel context.CancelCauseFunc, done <-chan struct{}) {
	ticker := time.NewTicker(api.EnvironmentGitOpsQualificationRuntimeCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
				// Storage and the context timer can observe expiry in either
				// order. Retain the deadline cause when validation wins that race.
				if claimed.LeaseUntil != nil && !time.Now().Before(*claimed.LeaseUntil) {
					err = errors.Join(err, context.DeadlineExceeded)
				}
				cancel(err)
				return
			}
		}
	}
}
