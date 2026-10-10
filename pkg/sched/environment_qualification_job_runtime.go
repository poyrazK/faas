package sched

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// WithEnvironmentWorkloadQualificationJob runs one reviewed job candidate in
// a private attempt, records only a sanitized passing result, and retires the
// attempt before returning. It never creates a customer JobRun or task record.
func (e *Engine) WithEnvironmentWorkloadQualificationJob(ctx context.Context,
	claimed state.EnvironmentWorkloadQualificationRequest) (result state.EnvironmentQualificationJobSmokeEvidence, resultErr error) {
	return e.withEnvironmentWorkloadQualificationJob(ctx, claimed, nil)
}

func (e *Engine) withEnvironmentWorkloadQualificationJob(ctx context.Context,
	claimed state.EnvironmentWorkloadQualificationRequest, beforeStart func(context.Context, state.Instance) error) (result state.EnvironmentQualificationJobSmokeEvidence, resultErr error) {
	qualifier, claimOK := e.store.(state.EnvironmentGitOpsQualificationStore)
	admitter, admissionOK := e.store.(state.EnvironmentGitOpsQualificationInstanceStore)
	publisher, publishOK := e.store.(state.EnvironmentGitOpsQualificationRuntimeStore)
	executor, executionOK := e.store.(state.EnvironmentQualificationExecutionStore)
	receipts, receiptOK := e.store.(state.EnvironmentQualificationJobSmokeReceiptStore)
	configReceipts, configReceiptOK := e.store.(state.EnvironmentQualificationConfigReceiptStore)
	runtimeReceipts, runtimeReceiptOK := e.store.(state.RuntimeConfigReceiptStore)
	vm, nativeOK := e.vmm.(EnvironmentQualificationVMM)
	if !claimOK || !admissionOK || !publishOK || !executionOK || !receiptOK || !configReceiptOK || !runtimeReceiptOK || !nativeOK || claimed.ExecutionMode != api.ExecutionModeJob ||
		claimed.LeaseUntil == nil || claimed.ReservedInstanceID == "" {
		return result, state.ErrInvalidArgument
	}
	if len(claimed.FrozenInputs.ServiceBindings) != 0 && (!e.hasEnvironmentQualificationServiceProxy() || ctx.Value(qualificationGraphContextKey{}) != claimed.GraphID) {
		return result, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	if _, _, err := state.EnvironmentQualificationJobSmokePolicyFor(claimed); err != nil {
		return result, err
	}
	if e.jobVmmClient == nil || e.jobExitWaiter == nil {
		return result, fmt.Errorf("qualification job requires vmmd job boot and exit capabilities: %w", state.ErrConflict)
	}
	releaser, heldStartOK := e.jobVmmClient.(interface {
		ReleaseJobStart(context.Context, JobStartSpec) error
	})
	if !heldStartOK {
		return result, fmt.Errorf("qualification job requires held-start release capability: %w", state.ErrConflict)
	}
	claimed, err := qualifier.RenewEnvironmentWorkloadQualification(ctx, claimed, api.EnvironmentGitOpsJobQualificationLeaseDuration)
	if err != nil {
		return result, err
	}
	ctx = withRenewedQualificationGraphRequest(ctx, claimed)
	ctx, deadlineCancel := context.WithDeadline(WithScope(ctx, claimed.FrozenInputs.Scope), *claimed.LeaseUntil)
	defer deadlineCancel()
	release, err := e.lockQualificationApp(ctx, claimed.AppID)
	if err != nil {
		return result, err
	}
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()
	if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
		return result, err
	}
	app, acct, _, err := e.resolveAppForDeploy(ctx, claimed.AppID)
	if err != nil {
		return result, err
	}
	if !acct.MayDeploy() {
		return result, acct.DeployBlockedProblem()
	}
	dep, err := e.store.DeploymentByID(ctx, claimed.DeploymentID)
	if err != nil {
		return result, err
	}
	app, err = state.AppForDeploymentRuntime(app, dep)
	if err != nil || !dep.EnvironmentWorkloadHeld() {
		return result, errors.Join(state.ErrConflict, err)
	}
	if err := securityQuarantineErr(dep); err != nil {
		return result, err
	}
	preferredNodeID := app.NodeID
	graphNodeID, graphNodePinned := ctx.Value(qualificationGraphDispatchNodeContextKey{}).(string)
	if graphNodePinned && graphNodeID != "" {
		preferredNodeID = graphNodeID
	}
	placement, err := e.choosePlacementLocked(ctx, Request{AppID: app.ID, Plan: acct.Plan, RAMMB: app.RAMMB,
		VCPU: 1, CPUMillicores: effectiveAppCPUMillicores(app), MaxConcurrency: app.MaxConcurrency, PreferredNodeID: preferredNodeID})
	if err != nil {
		return result, err
	}
	if graphNodePinned && graphNodeID != "" && placement.NodeID != graphNodeID {
		return result, fmt.Errorf("qualification job placement escaped its private node: %w", state.ErrConflict)
	}
	admission, err := admitter.CreateEnvironmentWorkloadQualificationInstance(ctx, claimed, state.EnvironmentWorkloadQualificationPlacement{
		NodeID: placement.NodeID, WakeID: uuid.NewString(), RAMMB: app.RAMMB,
	})
	if err != nil {
		return result, err
	}
	if !admission.Created {
		return result, state.ErrConflict
	}
	ins, frame := admission.Instance, admission.Execution
	if frame.InstanceID != claimed.ReservedInstanceID || frame.RequestID != claimed.ID || frame.GraphID != claimed.GraphID ||
		frame.Attempt != claimed.Attempt || frame.NodeID != placement.NodeID || frame.Artifact != claimed.Artifact {
		release()
		locked = false
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, errors.Join(state.ErrConflict, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, admission.Execution, ins, false, nil))
	}
	jobSpec := AppSpec{}
	if err := e.prepareQualificationServiceBindings(ctx, claimed, frame.NodeID, &jobSpec); err != nil {
		release()
		locked = false
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, errors.Join(err, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, false, nil))
	}
	qualificationAPIEnv := append([]fcvm.APIEnvEntry(nil), jobSpec.APIEnv...)
	for key, value := range claimed.FrozenInputs.Variables {
		qualificationAPIEnv = append(qualificationAPIEnv, fcvm.APIEnvEntry{Key: key, Value: value})
	}
	jobEnv := make(map[string]string, len(qualificationAPIEnv))
	for _, entry := range qualificationAPIEnv {
		if _, duplicate := jobEnv[entry.Key]; duplicate {
			release()
			locked = false
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
			defer cleanupCancel()
			return result, errors.Join(state.ErrConflict, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, false, nil))
		}
		jobEnv[entry.Key] = entry.Value
	}
	configDigest, err := fcvm.QualificationAPIEnvSHA256(qualificationAPIEnv)
	if err != nil {
		release()
		locked = false
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, errors.Join(err, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, false, nil))
	}
	runtimeInputs, sealedEnv, err := e.qualificationJobRuntimeConfigInputs(ctx, acct.ID, runtimeReceipts, claimed)
	if err != nil {
		release()
		locked = false
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, errors.Join(err, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, false, nil))
	}
	if err := e.ledger.Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, DeploymentScope: dep.Scope,
		Plan: acct.Plan, RAMMB: app.RAMMB, VCPU: 1, CPUMillicores: effectiveAppCPUMillicores(app),
		MaxConcurrency: app.MaxConcurrency, NodeID: placement.NodeID, NodeCeilingMB: placement.CeilingMB,
		VCPUBudget: placement.VCPUBudget, CPUBudgetMillicores: placement.CPUBudgetMillicores}); err != nil {
		release()
		locked = false
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, errors.Join(err, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, false, nil))
	}
	ctx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go e.watchQualificationOwner(ctx, qualifier, claimed, cancel, done)
	if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
		cancel(err)
		close(done)
		release()
		locked = false
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, false, err)
	}
	release()
	locked = false
	bootCtx, bootCancel := context.WithTimeout(ctx, e.budgetFor(state.StateColdBooting))
	if err := executor.MarkEnvironmentQualificationDispatched(bootCtx, claimed, frame); err != nil {
		bootCancel()
		cancel(nil)
		close(done)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, false, err)
	}
	bootCancel()
	if err := e.validateQualificationOwner(ctx, qualifier, claimed); err != nil {
		cancel(err)
		close(done)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
		defer cleanupCancel()
		return result, e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, true, err)
	}
	result, resultErr = e.runEnvironmentQualificationJob(ctx, claimed, frame, acct.ID, acct.Plan, jobEnv, sealedEnv.Entries, func(startCtx context.Context, bootInstance state.Instance) error {
		if err := e.validateQualificationOwner(startCtx, qualifier, claimed); err != nil {
			return err
		}
		ins, err = publisher.PublishEnvironmentWorkloadQualificationRuntime(startCtx, claimed, state.EnvironmentWorkloadQualificationRuntime{
			NodeID: bootInstance.NodeID, WakeID: bootInstance.WakeID, Netns: bootInstance.Netns, HostIP: bootInstance.HostIP,
			GuestUID: bootInstance.GuestUID, Inputs: runtimeInputs,
		})
		if err != nil {
			return fmt.Errorf("publish held qualification job runtime identity: %w", err)
		}
		if _, err := configReceipts.RecordEnvironmentQualificationConfigReceipt(startCtx, claimed, frame, configDigest); err != nil {
			return fmt.Errorf("persist held qualification job configuration acknowledgement: %w", err)
		}
		e.recordQualificationInstanceTransition(startCtx, ins, state.StateColdBooting, state.StateRunning, "environment_job_qualification")
		if err := e.validateQualificationOwner(startCtx, qualifier, claimed); err != nil {
			return err
		}
		if beforeStart != nil {
			if err := beforeStart(startCtx, ins); err != nil {
				return err
			}
		}
		if err := e.validateQualificationOwner(startCtx, qualifier, claimed); err != nil {
			return err
		}
		fresh, err := runtimeReceipts.RuntimeConfigInputsFresh(startCtx, claimed.AppID, runtimeInputs)
		if err != nil {
			return fmt.Errorf("recheck held qualification job configuration: %w", err)
		}
		if !fresh {
			return fmt.Errorf("qualification job configuration changed before start release: %w", state.ErrEnvironmentWorkloadPreparationUnavailable)
		}
		releaseCtx, releaseCancel := context.WithTimeout(startCtx, 5*time.Second)
		defer releaseCancel()
		if err := releaser.ReleaseJobStart(releaseCtx, JobStartSpec{NodeID: frame.NodeID, InstanceID: frame.InstanceID}); err != nil {
			return fmt.Errorf("release held qualification job: %w", err)
		}
		return nil
	})
	if resultErr == nil {
		resultErr = e.validateQualificationOwner(ctx, qualifier, claimed)
	}
	cancel(nil)
	close(done)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*DestroyTimeout)
	cleanupErr := e.retireEnvironmentQualificationJob(cleanupCtx, claimed.AppID, vm, executor, frame, ins, true, nil)
	cleanupCancel()
	if cleanupErr != nil {
		return state.EnvironmentQualificationJobSmokeEvidence{}, errors.Join(resultErr, cleanupErr)
	}
	if resultErr != nil {
		return state.EnvironmentQualificationJobSmokeEvidence{}, resultErr
	}
	receiptCtx, receiptCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer receiptCancel()
	if _, err := receipts.RecordEnvironmentQualificationJobSmokeReceipt(receiptCtx, claimed, result); err != nil {
		return state.EnvironmentQualificationJobSmokeEvidence{}, fmt.Errorf("persist retired job smoke receipt: %w", err)
	}
	return result, nil
}

func (e *Engine) retireEnvironmentQualificationJob(ctx context.Context, appID string, vm EnvironmentQualificationVMM,
	executor state.EnvironmentQualificationExecutionStore, frame state.EnvironmentQualificationExecution,
	ins state.Instance, dispatched bool, prior error) error {
	proof := state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}
	if dispatched {
		evidence, err := vm.RetireEnvironmentQualification(ctx, frame)
		if err != nil || evidence.Execution != frame {
			return errors.Join(prior, fmt.Errorf("qualification job native retirement: %w", errors.Join(err, state.ErrConflict)))
		}
		proof = evidence.Retirement
	}
	release, err := e.lockQualificationApp(ctx, appID)
	if err != nil {
		return errors.Join(prior, fmt.Errorf("qualification job retirement app lock: %w", err))
	}
	defer release()
	if err := executor.RetireEnvironmentQualificationExecution(ctx, frame, proof); err != nil {
		return errors.Join(prior, fmt.Errorf("qualification job execution retirement: %w", err))
	}
	e.releaseHostPortLeases(ctx, frame.NodeID, frame.InstanceID)
	from := state.State(ins.State)
	if from != state.StateRunning {
		from = state.StateColdBooting
	}
	e.recordQualificationInstanceTransition(ctx, ins, from, state.StateStopped, "environment_job_qualification_retired")
	e.ledger.Release(ins.ID)
	return prior
}

func (e *Engine) qualificationJobRuntimeConfigInputs(ctx context.Context, accountID string, receipts state.RuntimeConfigReceiptStore,
	claimed state.EnvironmentWorkloadQualificationRequest) (state.RuntimeConfigInputs, sealedEnvDelivery, error) {
	scope := normalizedDeploymentScope(claimed.FrozenInputs.Scope)
	boundary, stamped, err := state.RuntimeConfigChangedAtForScope(ctx, e.store, claimed.AppID, scope)
	if err != nil {
		return state.RuntimeConfigInputs{}, sealedEnvDelivery{}, err
	}
	if !stamped {
		boundary = time.Unix(0, 0).UTC()
	}
	variables := maps.Clone(claimed.FrozenInputs.Variables)
	if variables == nil {
		variables = map[string]string{}
	}
	secretRefReader, ok := e.store.(state.AppEnvironmentSecretReferenceReader)
	if !ok {
		return state.RuntimeConfigInputs{}, sealedEnvDelivery{}, fmt.Errorf("qualification job secret reference lookup is unavailable")
	}
	currentRefs, err := secretRefReader.AppEnvironmentSecretReferences(ctx, accountID, claimed.AppID, scope)
	if err != nil {
		return state.RuntimeConfigInputs{}, sealedEnvDelivery{}, fmt.Errorf("load qualification job secret references: %w", err)
	}
	if !maps.Equal(currentRefs, claimed.FrozenInputs.SecretRefs) {
		return state.RuntimeConfigInputs{}, sealedEnvDelivery{}, fmt.Errorf("qualification job secret references changed after candidate preparation: %w", state.ErrEnvironmentWorkloadPreparationUnavailable)
	}
	delivery, err := e.resolveSealedEnvDeliveryForRoleWithEmptyAll(ctx, accountID, claimed.AppID, scope,
		claimed.FrozenInputs.SecretRefs, false, false, false)
	if err != nil {
		return state.RuntimeConfigInputs{}, sealedEnvDelivery{}, fmt.Errorf("resolve qualification job secret references: %w", err)
	}
	inputs := state.RuntimeConfigInputs{Scope: scope, Boundary: boundary, Variables: variables,
		SecretVersions: map[string]int64{}, SecretRefs: maps.Clone(delivery.References)}
	addRuntimeSecretVersions(&inputs, delivery.Candidates, false)
	fresh, err := receipts.RuntimeConfigInputsFresh(ctx, claimed.AppID, inputs)
	if err != nil {
		return state.RuntimeConfigInputs{}, sealedEnvDelivery{}, err
	}
	if !fresh {
		return state.RuntimeConfigInputs{}, sealedEnvDelivery{}, fmt.Errorf("qualification job cannot acknowledge undelivered app configuration: %w", state.ErrEnvironmentWorkloadPreparationUnavailable)
	}
	return inputs, delivery, nil
}
