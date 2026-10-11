package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type EnvironmentQualificationVisitor func(context.Context, state.EnvironmentWorkloadQualificationRequest, state.Instance) error

type EnvironmentQualificationGraphVisitor func(context.Context, map[string]state.Instance) error

// EnvironmentQualificationSmokeVisitor must return one explicit passing,
// policy- and result-digested smoke record for every restored graph member.
// An error, missing member, duplicate member or failed check leaves all smoke
// evidence absent for this dispatch.
type EnvironmentQualificationSmokeVisitor func(context.Context, map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error)

type EnvironmentQualificationDispatchPage struct {
	Examined   int
	Claimed    int
	Executed   int
	Skipped    int
	NextCursor string
}

type EnvironmentQualificationGraphDispatchPage struct {
	Examined   int
	Claimed    int
	Executed   int
	Restored   int
	Skipped    int
	NextCursor string
}

// Dispatch consumes one bounded durable page, independently of notifications.
// Claim rechecks the graph and node owner; runtime rechecks before native effects.
// Each visit is lease-bounded and its original VM is retired before advancing.
// Executed means the visitor and retirement returned successfully, not that the
// graph is qualified. Smoke, snapshot and activation authority remain separate.
// Production does not invoke this consumer until those evidence gates exist.
func (e *Engine) DispatchEnvironmentWorkloadQualifications(ctx context.Context, nodeID, workerID, afterRequestID string, limit int, visit EnvironmentQualificationVisitor) (page EnvironmentQualificationDispatchPage, result error) {
	store, ok := e.store.(state.EnvironmentGitOpsQualificationDispatchStore)
	if !ok || visit == nil || !e.qualificationDispatchArgumentsValid(nodeID, workerID, afterRequestID, limit) {
		return page, state.ErrInvalidArgument
	}
	if !e.qualificationDispatchCapabilitiesAvailable() {
		return page, fmt.Errorf("qualification dispatch requires attempt-aware execution and retirement: %w", state.ErrConflict)
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	ids, err := store.ListEnvironmentWorkloadQualificationsForDispatch(ctx, nodeID, afterRequestID, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = afterRequestID
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return page, errors.Join(result, err)
		}
		page.Examined++
		page.NextCursor = id
		claimed, err := store.ClaimEnvironmentWorkloadQualificationForNode(ctx, id, nodeID, workerID, api.EnvironmentGitOpsQualificationLeaseDuration)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			page.Skipped++
			continue
		}
		if err == nil {
			page.Claimed++
			err = e.WithEnvironmentWorkloadQualificationRuntime(ctx, claimed, func(ctx context.Context, ins state.Instance) error {
				return visit(ctx, claimed, ins)
			})
			if err == nil {
				page.Executed++
			}
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("qualification dispatch request %s: %w", id, err))
		}
	}
	if len(ids) < limit {
		page.NextCursor = ""
	}
	return page, result
}

// Dispatch HTTP qualification graphs as a single durable cohort. Each graph gets
// all member attempts atomically before the private dependency-first execution
// window starts. A successful visitor is followed by durable per-member capture
// while the cohort is live; success still does not qualify or activate it.
func (e *Engine) DispatchEnvironmentWorkloadQualificationGraphs(ctx context.Context, nodeID, workerID, afterGraphID string, limit int, visit EnvironmentQualificationGraphVisitor) (page EnvironmentQualificationGraphDispatchPage, result error) {
	return e.dispatchEnvironmentWorkloadQualificationGraphs(ctx, nodeID, workerID, afterGraphID, limit, visit, nil)
}

// DispatchEnvironmentWorkloadQualificationGraphsWithRestore first runs the
// reviewed graph, captures it only after the caller visitor succeeds, retires
// that whole source cohort, then opens a distinct restored cohort for a second
// smoke visitor. The two visitors have separate instance identities and both
// windows are bounded by the same durable graph lease. A successful method is
// still not durable qualification or activation evidence.
func (e *Engine) DispatchEnvironmentWorkloadQualificationGraphsWithRestore(ctx context.Context, nodeID, workerID, afterGraphID string, limit int, visit EnvironmentQualificationGraphVisitor, visitRestored EnvironmentQualificationSmokeVisitor) (page EnvironmentQualificationGraphDispatchPage, result error) {
	if visitRestored == nil {
		return page, state.ErrInvalidArgument
	}
	return e.dispatchEnvironmentWorkloadQualificationGraphs(ctx, nodeID, workerID, afterGraphID, limit, visit, visitRestored)
}

// DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth is the
// fail-closed internal path for candidates that declare the supported HTTP
// health policy. It checks every live source member before capture, then checks
// every distinct restored member before smoke receipts are written. It remains
// an internal primitive; production qualification polling is a separate gate.
func (e *Engine) DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth(ctx context.Context, nodeID, workerID, afterGraphID string, limit int) (EnvironmentQualificationGraphDispatchPage, error) {
	return e.DispatchEnvironmentWorkloadQualificationGraphsWithRestore(ctx, nodeID, workerID, afterGraphID, limit,
		e.ProbeEnvironmentQualificationSourceGraph, e.ProbeEnvironmentQualificationGraph)
}

func (e *Engine) dispatchEnvironmentWorkloadQualificationGraphs(ctx context.Context, nodeID, workerID, afterGraphID string, limit int,
	visit EnvironmentQualificationGraphVisitor, visitRestored EnvironmentQualificationSmokeVisitor) (page EnvironmentQualificationGraphDispatchPage, result error) {
	store, ok := e.store.(state.EnvironmentGitOpsQualificationGraphDispatchStore)
	if !ok || visit == nil || !e.qualificationDispatchArgumentsValid(nodeID, workerID, afterGraphID, limit) {
		return page, state.ErrInvalidArgument
	}
	if !e.qualificationDispatchCapabilitiesAvailable() {
		return page, fmt.Errorf("qualification graph dispatch requires capture and attempt-aware retirement adapters: %w", state.ErrConflict)
	}
	if !e.qualificationGraphCaptureCapabilitiesAvailable() {
		return page, fmt.Errorf("qualification graph dispatch requires durable capture receipts: %w", state.ErrConflict)
	}
	if _, ok := e.store.(state.EnvironmentQualificationServiceStore); !ok {
		return page, fmt.Errorf("qualification graph dispatch requires scoped service-route verification: %w", state.ErrConflict)
	}
	if visitRestored != nil && !e.qualificationGraphRestoreCapabilitiesAvailable() {
		return page, fmt.Errorf("qualification graph restore dispatch requires dedicated restore admission, runtime publication and native restore: %w", state.ErrConflict)
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	ids, err := store.ListEnvironmentWorkloadQualificationGraphsForDispatch(ctx, nodeID, afterGraphID, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = afterGraphID
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return page, errors.Join(result, err)
		}
		page.Examined++
		page.NextCursor = id
		claimed, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(ctx, id, nodeID, workerID, api.EnvironmentGitOpsQualificationLeaseDuration)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			page.Skipped++
			continue
		}
		if err == nil {
			page.Claimed++
			graphCtx := context.WithValue(ctx, qualificationGraphDispatchNodeContextKey{}, nodeID)
			hasJob := false
			for _, request := range claimed {
				hasJob = hasJob || request.ExecutionMode == api.ExecutionModeJob
			}
			var qualifier state.EnvironmentGitOpsQualificationStore
			if hasJob {
				// Validate every edge and job contract before any native effect.
				// Bound jobs execute in dependency order with their target services
				// live and their own start gate closed until the route is resolved.
				if _, err = qualificationGraphExecutionOrder(claimed); err == nil {
					var ok bool
					qualifier, ok = e.store.(state.EnvironmentGitOpsQualificationStore)
					if !ok {
						err = state.ErrConflict
					} else {
						err = renewQualificationGraphClaims(ctx, qualifier, claimed)
					}
				}
			}
			runtimeRequests := make([]state.EnvironmentWorkloadQualificationRequest, 0, len(claimed))
			for _, request := range claimed {
				if request.ExecutionMode != api.ExecutionModeJob {
					runtimeRequests = append(runtimeRequests, request)
				}
			}
			if err == nil && len(claimed) > 0 {
				err = e.WithEnvironmentQualificationGraphRuntimes(graphCtx, claimed, func(graphCtx context.Context, instances map[string]state.Instance) error {
					if err := e.validateQualificationGraphServiceRoutes(graphCtx, instances); err != nil {
						return fmt.Errorf("validate qualification graph service routes: %w", err)
					}
					if len(runtimeRequests) > 0 {
						if err := visit(graphCtx, instances); err != nil {
							return fmt.Errorf("visit qualification graph: %w", err)
						}
						if err := e.CaptureEnvironmentWorkloadQualificationGraph(graphCtx, runtimeRequests, instances); err != nil {
							return fmt.Errorf("capture qualification graph: %w", err)
						}
						return nil
					}
					return nil
				})
			}
			if err == nil && visitRestored != nil && len(runtimeRequests) > 0 {
				// The source runner has returned only after reverse-order native
				// retirement. Restore therefore cannot overlap or adopt a capture VM.
				var evidence []state.EnvironmentQualificationSmokeEvidence
				err = e.WithEnvironmentQualificationGraphRestoreRuntimes(graphCtx, runtimeRequests, func(ctx context.Context, instances map[string]state.Instance) error {
					if err := e.validateQualificationGraphServiceRoutes(ctx, instances); err != nil {
						return err
					}
					var visitErr error
					evidence, visitErr = visitRestored(ctx, instances)
					if visitErr != nil {
						return visitErr
					}
					return validateQualificationSmokeEvidence(runtimeRequests, instances, evidence)
				})
				if err == nil {
					receipts := e.store.(state.EnvironmentQualificationSmokeReceiptStore)
					for _, request := range runtimeRequests {
						var report state.EnvironmentQualificationSmokeEvidence
						for _, candidate := range evidence {
							if candidate.Resource == request.Resource {
								report = candidate
								break
							}
						}
						if _, receiptErr := receipts.RecordEnvironmentQualificationSmokeReceipt(graphCtx, request, report); receiptErr != nil {
							err = errors.Join(err, fmt.Errorf("record qualification smoke for request %s: %w", request.ID, receiptErr))
						}
					}
					if err == nil {
						err = e.retireEnvironmentQualificationGraphArtifacts(graphCtx, runtimeRequests)
					}
					if err == nil {
						page.Restored++
					}
				}
			}
			if err == nil {
				page.Executed++
			}
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("qualification graph dispatch %s: %w", id, err))
		}
	}
	if len(ids) < limit {
		page.NextCursor = ""
	}
	return page, result
}

func renewQualificationGraphClaims(ctx context.Context, qualifier state.EnvironmentGitOpsQualificationStore,
	claimed []state.EnvironmentWorkloadQualificationRequest) error {
	for i := range claimed {
		renewed, err := qualifier.RenewEnvironmentWorkloadQualification(ctx, claimed[i], api.EnvironmentGitOpsJobQualificationLeaseDuration)
		if err != nil {
			return err
		}
		claimed[i] = renewed
	}
	return nil
}

func validateQualificationSmokeEvidence(claimed []state.EnvironmentWorkloadQualificationRequest, instances map[string]state.Instance,
	evidence []state.EnvironmentQualificationSmokeEvidence) error {
	if len(claimed) == 0 || len(instances) != len(claimed) || len(evidence) != len(claimed) {
		return fmt.Errorf("restored smoke must cover every graph member exactly once: %w", state.ErrConflict)
	}
	seenResources := make(map[string]bool, len(evidence))
	seenInstances := make(map[string]bool, len(claimed))
	for _, request := range claimed {
		instance, ok := instances[request.Resource]
		if !ok || instance.ID == "" || seenInstances[instance.ID] {
			return fmt.Errorf("restored smoke graph has a missing or reused instance for %s: %w", request.Resource, state.ErrConflict)
		}
		seenInstances[instance.ID] = true
	}
	for _, report := range evidence {
		if seenResources[report.Resource] {
			return fmt.Errorf("restored smoke contains duplicate resource %s: %w", report.Resource, state.ErrConflict)
		}
		seenResources[report.Resource] = true
		var request *state.EnvironmentWorkloadQualificationRequest
		for i := range claimed {
			if claimed[i].Resource == report.Resource {
				request = &claimed[i]
				break
			}
		}
		if request == nil {
			return fmt.Errorf("restored smoke contains an unexpected resource %s: %w", report.Resource, state.ErrConflict)
		}
		instance := instances[request.Resource]
		if err := report.ValidateFor(*request, instance.ID); err != nil {
			return fmt.Errorf("restored smoke evidence for %s: %w", request.Resource, err)
		}
	}
	return nil
}

func (e *Engine) qualificationGraphCaptureCapabilitiesAvailable() bool {
	_, store := e.store.(state.EnvironmentQualificationSnapshotStore)
	_, vmm := e.vmm.(EnvironmentQualificationSnapshotVMM)
	return store && vmm
}

func (e *Engine) qualificationGraphRestoreCapabilitiesAvailable() bool {
	_, store := e.store.(state.EnvironmentQualificationRestoreStore)
	_, runtime := e.store.(state.EnvironmentQualificationRestoreRuntimeStore)
	_, receipt := e.store.(state.EnvironmentQualificationRestoreReceiptStore)
	_, smoke := e.store.(state.EnvironmentQualificationSmokeReceiptStore)
	_, snapshot := e.store.(state.EnvironmentQualificationSnapshotStore)
	_, execution := e.store.(state.EnvironmentQualificationExecutionStore)
	_, native := e.vmm.(EnvironmentQualificationRestoreVMM)
	_, retirement := e.vmm.(EnvironmentQualificationVMM)
	_, artifactRetirement := e.vmm.(EnvironmentQualificationArtifactRetirementVMM)
	return store && runtime && receipt && smoke && snapshot && execution && native && retirement && artifactRetirement
}

func (e *Engine) retireEnvironmentQualificationGraphArtifacts(ctx context.Context, claimed []state.EnvironmentWorkloadQualificationRequest) error {
	snapshots, snapshotOK := e.store.(state.EnvironmentQualificationSnapshotStore)
	restores, restoreOK := e.store.(state.EnvironmentQualificationRestoreReceiptStore)
	smokes, smokeOK := e.store.(state.EnvironmentQualificationSmokeReceiptStore)
	executions, executionOK := e.store.(state.EnvironmentQualificationExecutionStore)
	retirer, retireOK := e.vmm.(EnvironmentQualificationArtifactRetirementVMM)
	if !snapshotOK || !restoreOK || !smokeOK || !executionOK || !retireOK || len(claimed) == 0 {
		return state.ErrConflict
	}
	for _, request := range claimed {
		capture, err := snapshots.EnvironmentQualificationSnapshotReceipt(ctx, request.ReservedInstanceID)
		if err != nil {
			return err
		}
		captureStatus, err := executions.EnvironmentQualificationExecution(ctx, capture.Execution.InstanceID)
		if err != nil || captureStatus.Execution != capture.Execution || captureStatus.RetiredAt == nil || captureStatus.Retirement == nil ||
			captureStatus.Retirement.Kind != state.QualificationNativeRetired || !captureStatus.Retirement.ProcessesExited || !captureStatus.Retirement.ResourcesRemoved ||
			captureStatus.Retirement.ReceiptID != capture.Snapshot.CaptureID || captureStatus.Retirement.NativeGeneration != capture.Snapshot.NativeGeneration ||
			captureStatus.Retirement.KernelBootID != capture.Snapshot.KernelBootID {
			return errors.Join(state.ErrConflict, err)
		}
		restore, err := restores.EnvironmentQualificationRestoreReceipt(ctx, request.ID, request.Attempt)
		if err != nil {
			return err
		}
		restoreStatus, err := executions.EnvironmentQualificationExecution(ctx, restore.InstanceID)
		if err != nil || restoreStatus.Execution.InstanceID != restore.InstanceID || restoreStatus.Execution.CaptureInstanceID != request.ReservedInstanceID ||
			restoreStatus.Execution.RequestID != request.ID || restoreStatus.Execution.Attempt != request.Attempt || restoreStatus.RetiredAt == nil ||
			restoreStatus.Retirement == nil || restoreStatus.Retirement.Kind != state.QualificationNativeRetired ||
			!restoreStatus.Retirement.ProcessesExited || !restoreStatus.Retirement.ResourcesRemoved ||
			restoreStatus.Retirement.KernelBootID != capture.Snapshot.KernelBootID ||
			restoreStatus.Retirement.NativeGeneration == capture.Snapshot.NativeGeneration {
			return errors.Join(state.ErrConflict, err)
		}
		smoke, err := smokes.EnvironmentQualificationSmokeReceipt(ctx, request.ID, request.Attempt)
		if err != nil {
			return err
		}
		if smoke.RequestID != request.ID || smoke.Attempt != request.Attempt || smoke.GraphID != request.GraphID ||
			smoke.CaptureInstanceID != request.ReservedInstanceID || smoke.InstanceID != restore.InstanceID || smoke.Resource != request.Resource {
			return state.ErrConflict
		}
		if err := retirer.RetireEnvironmentQualificationArtifacts(ctx, capture.Execution, restoreStatus.Execution, smoke, capture.Snapshot.CaptureID); err != nil {
			return fmt.Errorf("retire qualification capture artifacts for %s: %w", request.Resource, err)
		}
	}
	return ctx.Err()
}

func (e *Engine) qualificationDispatchCapabilitiesAvailable() bool {
	_, claim := e.store.(state.EnvironmentGitOpsQualificationStore)
	_, admission := e.store.(state.EnvironmentGitOpsQualificationInstanceStore)
	_, runtime := e.store.(state.EnvironmentGitOpsQualificationRuntimeStore)
	_, execution := e.store.(state.EnvironmentQualificationExecutionStore)
	_, native := e.vmm.(EnvironmentQualificationVMM)
	return claim && admission && runtime && execution && native
}

func (e *Engine) qualificationDispatchArgumentsValid(nodeID, workerID, cursor string, limit int) bool {
	node, err := uuid.Parse(nodeID)
	if err != nil || node == uuid.Nil || workerID == "" || workerID != strings.TrimSpace(workerID) ||
		len(workerID) > api.EnvironmentGitOpsQualificationWorkerIDMaxBytes || limit < 1 || limit > api.EnvironmentGitOpsQualificationDispatchBatchMax {
		return false
	}
	if cursor != "" {
		if id, err := uuid.Parse(cursor); err != nil || id == uuid.Nil {
			return false
		}
	}
	owner := e.ownerNodeID
	if owner == "" {
		owner = e.defaultLocalNodeID
	}
	ownerID, err := uuid.Parse(owner)
	return err == nil && ownerID == node
}
