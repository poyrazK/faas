package sched

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// WithEnvironmentWorkloadQualificationRestoreRuntime runs one captured
// workload on a distinct native restore target. It keeps the same reviewed
// attempt and pinned node as the retired capture, publishes only the target's
// runtime receipt, and always joins target retirement before returning.
// Visitor success is still only smoke evidence; it never activates a graph.
func (e *Engine) WithEnvironmentWorkloadQualificationRestoreRuntime(ctx context.Context, claimed state.EnvironmentWorkloadQualificationRequest, visit func(context.Context, state.Instance) error) (result error) {
	qualifier, ok := e.store.(state.EnvironmentGitOpsQualificationStore)
	restoreAdmission, admissionOK := e.store.(state.EnvironmentQualificationRestoreStore)
	restorePublisher, publishOK := e.store.(state.EnvironmentQualificationRestoreRuntimeStore)
	restoreReceipts, receiptOK := e.store.(state.EnvironmentQualificationRestoreReceiptStore)
	snapshots, snapshotOK := e.store.(state.EnvironmentQualificationSnapshotStore)
	executor, executionOK := e.store.(state.EnvironmentQualificationExecutionStore)
	vm, nativeOK := e.vmm.(EnvironmentQualificationRestoreVMM)
	retirer, retirementOK := e.vmm.(EnvironmentQualificationVMM)
	if !ok || !admissionOK || !publishOK || !receiptOK || !snapshotOK || !executionOK || !nativeOK || visit == nil ||
		!retirementOK || claimed.LeaseUntil == nil || claimed.ReservedInstanceID == "" {
		return state.ErrInvalidArgument
	}
	if len(claimed.FrozenInputs.ServiceBindings) != 0 &&
		(!e.hasEnvironmentQualificationServiceProxy() || ctx.Value(qualificationGraphContextKey{}) != claimed.GraphID) {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
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
	if err := e.validateQualificationRestoreOwner(ctx, qualifier, claimed); err != nil {
		return fmt.Errorf("qualification restore owner validation: %w", err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	defer close(done)
	go e.watchQualificationRestoreOwner(ctx, qualifier, claimed, cancel, done)

	original, err := executor.EnvironmentQualificationExecution(ctx, claimed.ReservedInstanceID)
	if err != nil {
		return err
	}
	capture, err := snapshots.EnvironmentQualificationSnapshotReceipt(ctx, claimed.ReservedInstanceID)
	if err != nil {
		return err
	}
	if !qualificationRestoreSourceMatches(claimed, original, capture) {
		return fmt.Errorf("qualification restore source is not a retired, receipt-backed capture: %w", state.ErrConflict)
	}
	app, acct, limits, err := e.resolveAppForDeploy(ctx, claimed.AppID)
	if err != nil {
		return fmt.Errorf("qualification restore resolve app: %w", err)
	}
	if !acct.MayDeploy() {
		return acct.DeployBlockedProblem()
	}
	dep, err := e.store.DeploymentByID(ctx, claimed.DeploymentID)
	if err != nil {
		return err
	}
	app, err = state.AppForDeploymentRuntime(app, dep)
	if err != nil || !dep.EnvironmentWorkloadHeld() || app.RAMMB != original.Execution.RAMMB {
		return fmt.Errorf("qualification restore app, deployment or RAM changed: %w", errors.Join(state.ErrConflict, err))
	}
	if err := securityQuarantineErr(dep); err != nil {
		return err
	}
	if claimed.ExecutionMode == api.ExecutionModeJob {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	preferredNodeID := original.Execution.NodeID
	graphNodeID, graphNodePinned := ctx.Value(qualificationGraphDispatchNodeContextKey{}).(string)
	if graphNodePinned && graphNodeID != "" && graphNodeID != preferredNodeID {
		return fmt.Errorf("qualification restore source escaped its private graph node: %w", state.ErrConflict)
	}
	cpu := effectiveAppCPUMillicores(app)
	startupCPU := cpu
	if !dep.DisableStartupCPUBoost {
		startupCPU = startupCPUBoostQuota(acct.Plan, cpu)
	}
	placement, err := e.choosePlacementLocked(ctx, Request{AppID: app.ID, Plan: acct.Plan, RAMMB: app.RAMMB,
		VCPU: limits.VCPU, CPUMillicores: startupCPU, MaxConcurrency: app.MaxConcurrency, PreferredNodeID: preferredNodeID})
	if err != nil {
		return fmt.Errorf("qualification restore placement: %w", err)
	}
	if placement.NodeID != preferredNodeID || app.RAMMB != original.Execution.RAMMB {
		return fmt.Errorf("qualification restore must remain on the original capture node and RAM reservation: %w", state.ErrConflict)
	}
	ctx, err = e.preflightQualificationGraphServiceListener(ctx, placement.NodeID)
	if err != nil {
		return err
	}
	wakeID := uuid.NewString()
	admission, err := restoreAdmission.CreateEnvironmentQualificationRestore(ctx, claimed, state.EnvironmentWorkloadQualificationPlacement{
		NodeID: placement.NodeID, WakeID: wakeID, RAMMB: app.RAMMB,
	})
	if err != nil {
		return fmt.Errorf("qualification restore admission: %w", err)
	}
	if !admission.Created {
		return fmt.Errorf("qualification restore admission replayed an existing target: %w", state.ErrConflict)
	}
	ins, frame, vmAttempted := admission.Instance, admission.Execution, false
	var restoreInputs state.RuntimeConfigInputs
	restorePublished := false
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		proof := state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}
		if vmAttempted {
			evidence, err := retirer.RetireEnvironmentQualification(cleanupCtx, frame)
			if err != nil || evidence.Execution != frame {
				result = errors.Join(result, fmt.Errorf("qualification restore target retirement: %w", errors.Join(err, state.ErrConflict)))
				return
			}
			proof = evidence.Retirement
		}
		if !locked {
			cleanupRelease, err := e.lockQualificationApp(cleanupCtx, claimed.AppID)
			if err != nil {
				result = errors.Join(result, fmt.Errorf("qualification restore retirement app lock: %w", err))
				return
			}
			defer cleanupRelease()
		}
		if err := executor.RetireEnvironmentQualificationExecution(cleanupCtx, frame, proof); err != nil {
			result = errors.Join(result, fmt.Errorf("qualification restore execution retirement for %s: %w", claimed.Resource, err))
			return
		}
		if restorePublished {
			if _, err := restoreReceipts.RecordEnvironmentQualificationRestoreReceipt(cleanupCtx, claimed, frame, restoreInputs); err != nil {
				result = errors.Join(result, fmt.Errorf("qualification restore evidence for %s: %w", claimed.Resource, err))
			}
		}
		e.releaseHostPortLeases(cleanupCtx, frame.NodeID, frame.InstanceID)
		e.recordQualificationInstanceTransition(cleanupCtx, ins, state.State(ins.State), state.StateStopped, "environment_qualification_restore_retired")
		e.ledger.Release(ins.ID)
	}()
	if admission.Execution.CaptureInstanceID != claimed.ReservedInstanceID || admission.Capture.Execution != capture.Execution ||
		admission.Capture.Snapshot != capture.Snapshot {
		return fmt.Errorf("qualification restore admission changed its capture: %w", state.ErrConflict)
	}
	if startupCPU > cpu {
		if err := e.store.SetInstanceStartupCPUBoostUntil(ctx, ins.ID, claimed.LeaseUntil); err != nil {
			return fmt.Errorf("qualification restore: persist startup CPU reservation: %w", err)
		}
	}
	e.emitInstanceChanged(ctx, ins.ID, app.ID, state.StateColdBooting, ins.WakeID)
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
	if ctx.Value(qualificationGraphContextKey{}) == claimed.GraphID && prepared.Spec.AppProtocol != "" && prepared.Spec.AppProtocol != api.AppProtocolHTTP1 {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	if err := e.prepareQualificationServiceBindings(ctx, claimed, placement.NodeID, &prepared.Spec); err != nil {
		return err
	}
	configDigest, err := fcvm.QualificationAPIEnvSHA256(prepared.Spec.APIEnv)
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
	if err := e.validateQualificationRestoreOwner(ctx, qualifier, claimed); err != nil {
		return err
	}
	release()
	locked = false
	bootCtx, bootCancel := context.WithTimeout(ctx, e.budgetFor(state.StateColdBooting))
	defer bootCancel()
	if err := restoreAdmission.MarkEnvironmentQualificationRestoreDispatched(bootCtx, claimed, frame); err != nil {
		return err
	}
	vmAttempted = true
	out, err := vm.RestoreEnvironmentQualification(bootCtx, frame, prepared.Spec)
	if err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	if out == nil || out.Instance != frame.InstanceID || out.Method != vmmdpb.WakeMethod_WAKE_RESTORE || out.RequestedMethod != vmmdpb.WakeMethod_WAKE_RESTORE {
		return fmt.Errorf("qualification restore used an unexpected target or lifecycle: %w", state.ErrConflict)
	}
	if err := e.validateQualificationRestoreOwner(ctx, qualifier, claimed); err != nil {
		return err
	}
	ins, err = restorePublisher.PublishEnvironmentQualificationRestoreRuntime(ctx, claimed, frame, state.EnvironmentWorkloadQualificationRuntime{
		NodeID: ins.NodeID, WakeID: ins.WakeID, Netns: out.Netns, HostIP: out.HostIP, GuestUID: int(out.LeaseUID), Inputs: prepared.Inputs,
	})
	if err != nil {
		ins = admission.Instance
		return err
	}
	receipts, ok := e.store.(state.EnvironmentQualificationConfigReceiptStore)
	if !ok {
		return state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	if _, err := receipts.RecordEnvironmentQualificationConfigReceipt(ctx, claimed, frame, configDigest); err != nil {
		return fmt.Errorf("persist restored guest qualification configuration acknowledgement: %w", err)
	}
	restoreInputs = prepared.Inputs
	restorePublished = true
	e.recordQualificationInstanceTransition(ctx, ins, state.StateColdBooting, state.StateRunning, "environment_qualification_restore")
	e.recordAppSecretDelivery(ctx, delivery, state.SecretDeliveryDelivered, "")
	deliveryFinalized = true
	if err := e.validateQualificationRestoreOwner(ctx, qualifier, claimed); err != nil {
		return err
	}
	if err := visit(ctx, ins); err != nil {
		return err
	}
	return e.validateQualificationRestoreOwner(ctx, qualifier, claimed)
}

// WithEnvironmentQualificationGraphRestoreRuntimes opens one restored graph
// cohort in dependency order. Service names resolve through the existing
// graph-scoped proxy, whose runtime receipt now points at each distinct target.
// Targets are retired in reverse dependency order even when the visitor fails.
func (e *Engine) WithEnvironmentQualificationGraphRestoreRuntimes(ctx context.Context, claimed []state.EnvironmentWorkloadQualificationRequest, visit func(context.Context, map[string]state.Instance) error) error {
	store, ok := e.store.(state.EnvironmentQualificationGraphStore)
	qualifier, claimOK := e.store.(state.EnvironmentGitOpsQualificationStore)
	_, configReceiptOK := e.store.(state.EnvironmentQualificationConfigReceiptStore)
	if !ok || !claimOK || !configReceiptOK || visit == nil || len(claimed) == 0 {
		return state.ErrInvalidArgument
	}
	persisted, err := store.EnvironmentQualificationGraphRequests(ctx, claimed[0])
	if err != nil {
		return err
	}
	receipts, _ := e.store.(state.EnvironmentQualificationJobSmokeReceiptStore)
	if err := validateQualificationGraphClaimSubset(ctx, persisted, claimed, receipts); err != nil {
		return err
	}
	for _, request := range claimed {
		if request.GraphID != claimed[0].GraphID {
			return state.ErrConflict
		}
		if err := e.validateQualificationRestoreOwner(ctx, qualifier, request); err != nil {
			return err
		}
	}
	ordered, err := qualificationGraphExecutionOrder(claimed)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, qualificationGraphContextKey{}, claimed[0].GraphID)
	ctx = context.WithValue(ctx, qualificationGraphRequestsContextKey{}, slices.Clone(claimed))
	instances := make(map[string]state.Instance, len(ordered))
	var execute func(context.Context, int) error
	execute = func(ctx context.Context, index int) error {
		if index == len(ordered) {
			return visit(ctx, instances)
		}
		request := ordered[index]
		return e.WithEnvironmentWorkloadQualificationRestoreRuntime(ctx, request, func(ctx context.Context, ins state.Instance) error {
			instances[request.Resource] = ins
			if nodeID, pinned := ctx.Value(qualificationGraphDispatchNodeContextKey{}).(string); !pinned || nodeID == "" {
				ctx = context.WithValue(ctx, qualificationGraphDispatchNodeContextKey{}, ins.NodeID)
			}
			return execute(ctx, index+1)
		})
	}
	return execute(ctx, 0)
}

// A restore deliberately starts from a retired source VM. Validate the live
// reviewed request and app owner here, while the separate restore admission
// validates the retained capture and its native retirement receipt.
func (e *Engine) validateQualificationRestoreOwner(ctx context.Context, qualifier state.EnvironmentGitOpsQualificationStore, claimed state.EnvironmentWorkloadQualificationRequest) error {
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
	return qualifier.ValidateEnvironmentWorkloadQualification(ctx, claimed)
}

func (e *Engine) watchQualificationRestoreOwner(ctx context.Context, qualifier state.EnvironmentGitOpsQualificationStore, claimed state.EnvironmentWorkloadQualificationRequest, cancel context.CancelCauseFunc, done <-chan struct{}) {
	ticker := time.NewTicker(api.EnvironmentGitOpsQualificationRuntimeCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.validateQualificationRestoreOwner(ctx, qualifier, claimed); err != nil {
				if claimed.LeaseUntil != nil && !time.Now().Before(*claimed.LeaseUntil) {
					err = errors.Join(err, context.DeadlineExceeded)
				}
				cancel(err)
				return
			}
		}
	}
}

func qualificationRestoreSourceMatches(claimed state.EnvironmentWorkloadQualificationRequest, status state.EnvironmentQualificationExecutionStatus, capture state.EnvironmentQualificationSnapshotReceipt) bool {
	frame, retirement := status.Execution, status.Retirement
	return claimed.ReservedInstanceID != "" && status.CaptureInstanceID == "" && status.DispatchStarted && status.RetiredAt != nil && retirement != nil &&
		retirement.Kind == state.QualificationNativeRetired && retirement.ProcessesExited && retirement.ResourcesRemoved &&
		frame.InstanceID == claimed.ReservedInstanceID && frame.RequestID == claimed.ID && frame.Attempt == claimed.Attempt &&
		frame.GraphID == claimed.GraphID && frame.AppID == claimed.AppID && frame.DeploymentID == claimed.DeploymentID &&
		frame.Resource == claimed.Resource && frame.Scope == claimed.FrozenInputs.Scope && frame.PlanHash == claimed.FrozenInputs.PlanHash &&
		frame.Artifact == claimed.Artifact && capture.Execution == frame &&
		retirement.NativeGeneration == capture.Snapshot.NativeGeneration && retirement.KernelBootID == capture.Snapshot.KernelBootID &&
		state.ValidateEnvironmentQualificationSnapshot(frame, capture.Snapshot) == nil
}
