package state

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// DataRepairAdmission contains only immutable plan metadata validated by the
// trusted executor. Source identity is reloaded under the admission lock.
type DataRepairAdmission struct {
	AccountID, AppID, ID, Binding string
	Plan                          api.OperationDataRepair
}

type DataRepairAdvance struct {
	ExpectedSequence       int64
	Phase                  string
	AppliedAt              *time.Time
	ReplacementOperationID string
	ApplyClosedAt          *time.Time
}

type DataRepairOperationStore interface {
	CreateDataRepairOperation(context.Context, DataRepairAdmission) (Operation, bool, error)
	AdvanceDataRepairOperation(context.Context, string, string, DataRepairAdvance) (Operation, error)
}

var repairHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateDataRepairAdmission(in DataRepairAdmission) error {
	for _, id := range []string{in.AccountID, in.AppID, in.ID, in.Plan.PlanID, in.Plan.SourceOperationID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return ErrInvalidArgument
		}
	}
	p := in.Plan
	if p.PlanID != in.ID || p.SourceOperationID == in.ID || !repairHash.MatchString(p.PlanDigest) || !repairHash.MatchString(in.Binding) ||
		p.Phase != "" || !p.PhaseStartedAt.IsZero() || p.Approval != nil || p.AppliedAt != nil || p.ApplyClosedAt != nil || p.RehearsalTaskID != "" || p.RehearsalStatus != "" || p.RehearsalCancelRequested || p.ReplacementOperationID != "" || p.Candidates < 0 || p.Candidates > api.DataRepairMaxRows || p.Conflicts < 0 || p.Conflicts > api.DataRepairMaxRows || p.Unavailable < 0 || p.Unavailable > api.DataRepairMaxRows ||
		p.Candidates+p.Conflicts+p.Unavailable > api.DataRepairMaxRows || p.PlanExpiresAt.IsZero() {
		return ErrInvalidArgument
	}
	if !validDataRepairParent(p) {
		return ErrInvalidArgument
	}
	return nil
}

func replayDataRepair(op Operation, in DataRepairAdmission) error {
	if op.DataRepair == nil || op.AppID != in.AppID || op.DataRepairBinding != in.Binding ||
		op.DataRepair.SourceOperationID != in.Plan.SourceOperationID || op.DataRepair.PlanDigest != in.Plan.PlanDigest || op.DataRepair.PlanID != in.Plan.PlanID ||
		op.DataRepair.ReplacesPlanID != in.Plan.ReplacesPlanID || op.DataRepair.ReplacesPlanDigest != in.Plan.ReplacesPlanDigest {
		return ErrOperationInputConflict
	}
	return nil
}

func newDataRepairOperation(source Operation, in DataRepairAdmission, limits api.OperationPlanLimits, now time.Time) (Operation, error) {
	if source.AccountID != in.AccountID || source.AppID != in.AppID || source.PlatformTenantID == "" {
		return Operation{}, ErrNotFound
	}
	if in.Plan.ReplacesPlanID == "" {
		if source.DataRepair != nil {
			return Operation{}, ErrNotFound
		}
		if !source.State.Terminal() {
			return Operation{}, ErrConflict
		}
	} else if source.ID != in.Plan.ReplacesPlanID || source.DataRepair == nil || source.DataRepair.PlanDigest != in.Plan.ReplacesPlanDigest ||
		source.DataRepair.SourceOperationID != in.Plan.SourceOperationID {
		return Operation{}, ErrConflict
	}
	if !in.Plan.PlanExpiresAt.After(now) && in.Plan.ReplacesPlanID == "" {
		return Operation{}, ErrConflict
	}
	p := in.Plan
	p.Phase = api.DataRepairPrepared
	p.PhaseStartedAt = now
	op := Operation{OperationResponse: api.OperationResponse{
		ID: in.ID, Name: "gregale-data-repair", Generation: 1, State: api.OperationAccepted, DataRepair: &p,
		LatestSequence: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Duration(limits.ResultRetentionSeconds) * time.Second),
		CompletionDelivery: api.OperationDeliveryResponse{State: "not_configured"},
	}, AccountID: source.AccountID, AppID: source.AppID, Scope: source.Scope, PlatformTenantID: source.PlatformTenantID,
		DeploymentID: source.DeploymentID, ReleaseID: source.ReleaseID, DataRepairID: in.ID, DataRepairBinding: in.Binding,
		PlanLimits: limits, EventExpiresAt: now.Add(time.Duration(limits.EventRetentionSeconds) * time.Second)}
	// A lost executor response may be recovered after the child's expiry. Admit
	// its historical identity and finish the parent fence without write authority.
	if !p.PlanExpiresAt.After(now) {
		op.State, op.FailureCode, op.DataRepair.Phase = api.OperationFailed, "repair_plan_expired", api.DataRepairFailed
	}
	return op, nil
}

func dataRepairEvent(op *Operation, kind string, now time.Time) api.OperationEvent {
	op.LatestSequence++
	op.UpdatedAt = now
	op.Progress = &api.OperationProgress{Stage: op.DataRepair.Phase, Total: int64(op.DataRepair.Candidates), Attempt: 1, UpdatedAt: now}
	if op.DataRepair.Phase == api.DataRepairSucceeded {
		op.Progress.Completed = op.Progress.Total
	}
	data, _ := json.Marshal(map[string]any{"state": op.State, "data_repair": op.DataRepair})
	return api.OperationEvent{OperationID: op.ID, Sequence: op.LatestSequence, Type: kind, ExecutionID: op.ID, Attempt: 1, Data: data, CreatedAt: now}
}

func advanceDataRepair(op *Operation, in DataRepairAdvance, now time.Time) (api.OperationEvent, error) {
	if op.DataRepair == nil {
		return api.OperationEvent{}, ErrNotFound
	}
	if op.LatestSequence != in.ExpectedSequence {
		return api.OperationEvent{}, ErrConflict
	}
	if in.Phase == api.DataRepairReplacing {
		return reserveDataRepairReplacement(op, in, now)
	}
	if in.ReplacementOperationID != "" {
		return api.OperationEvent{}, ErrInvalidArgument
	}
	from, to := op.DataRepair.Phase, in.Phase
	if from == api.DataRepairRehearsing && op.DataRepair.RehearsalTaskID != "" && (op.DataRepair.RehearsalStatus == RepairRehearsalQueued || op.DataRepair.RehearsalStatus == RepairRehearsalRunning || op.DataRepair.RehearsalStatus == RepairRehearsalCleanup || op.DataRepair.RehearsalStatus == RepairRehearsalPublishing) {
		return api.OperationEvent{}, ErrConflict
	}
	allowed := false
	switch to {
	case api.DataRepairRehearsing:
		allowed = from == api.DataRepairPrepared
	case api.DataRepairPrepared:
		allowed = from == api.DataRepairRehearsing
	case api.DataRepairAwaitingApproval:
		allowed = from == api.DataRepairRehearsing
	case api.DataRepairApplying:
		allowed = from == api.DataRepairApproved || from == api.DataRepairUnknown
	case api.DataRepairUnknown:
		allowed = from == api.DataRepairApplying
	case api.DataRepairResolving:
		allowed = from == api.DataRepairApplying || from == api.DataRepairUnknown
	case api.DataRepairClosed:
		allowed = from == api.DataRepairResolving && in.ApplyClosedAt != nil && !in.ApplyClosedAt.IsZero()
	case api.DataRepairSucceeded:
		allowed = (from == api.DataRepairApplying || from == api.DataRepairUnknown || from == api.DataRepairResolving) && in.AppliedAt != nil && !in.AppliedAt.IsZero()
	case api.DataRepairFailed:
		allowed = from == api.DataRepairPrepared || from == api.DataRepairRehearsing || from == api.DataRepairAwaitingApproval || from == api.DataRepairApproved
	}
	if !allowed || (in.AppliedAt != nil && to != api.DataRepairSucceeded) || (in.ApplyClosedAt != nil && to != api.DataRepairClosed) {
		return api.OperationEvent{}, ErrConflict
	}
	if (to == api.DataRepairRehearsing || to == api.DataRepairAwaitingApproval) && !now.Before(op.DataRepair.PlanExpiresAt) {
		return api.OperationEvent{}, ErrConflict
	}
	op.DataRepair.Phase = to
	op.DataRepair.PhaseStartedAt = now
	op.Waiting = nil
	kind := "progress"
	switch to {
	case api.DataRepairPrepared:
		op.State = api.OperationAccepted
	case api.DataRepairAwaitingApproval:
		op.State = api.OperationWaiting
		op.Waiting = &api.OperationWaitingState{Kind: api.OperationWaitApproval, Step: "repair", Since: now, Approver: api.OperationApproverAccount, Until: &op.DataRepair.PlanExpiresAt}
	case api.DataRepairUnknown:
		op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "repair_outcome_unknown", "reconciliation_required"
	case api.DataRepairSucceeded:
		op.State, op.FailureCode, kind = api.OperationSucceeded, "", "succeeded"
		op.DataRepair.AppliedAt = in.AppliedAt
	case api.DataRepairFailed:
		op.State, op.FailureCode, kind = api.OperationFailed, "repair_plan_expired", "failed"
	case api.DataRepairClosed:
		op.State, op.FailureCode, kind = api.OperationFailed, "repair_not_committed", "failed"
		op.DataRepair.ApplyClosedAt = in.ApplyClosedAt
	default:
		op.State, op.FailureCode = api.OperationRunning, ""
	}
	if from == api.DataRepairResolving && (to == api.DataRepairClosed || to == api.DataRepairSucceeded) {
		// A recovered reservation may outlive the original result window. Keep
		// its newly settled outcome reviewable under the usual retention bounds;
		// the source plan's fixed expiry and approval authority never change.
		op.ExpiresAt = now.Add(time.Duration(op.PlanLimits.ResultRetentionSeconds) * time.Second)
		op.EventExpiresAt = now.Add(time.Duration(op.PlanLimits.EventRetentionSeconds) * time.Second)
	}
	return dataRepairEvent(op, kind, now), nil
}

func decideDataRepairApproval(op *Operation, in OperationApprovalInput, now time.Time) (api.OperationEvent, bool, error) {
	if err := validateOperationApproval(in); err != nil {
		return api.OperationEvent{}, false, err
	}
	if op.DataRepair == nil || in.Step != "repair" {
		return api.OperationEvent{}, false, ErrNotFound
	}
	if in.ApproverKind != api.OperationApproverAccount || in.PlatformTenantID != "" {
		return api.OperationEvent{}, false, ErrOperationApproverMismatch
	}
	if in.ExpectedGeneration != op.Generation || in.Actor.ID == "" {
		return api.OperationEvent{}, false, ErrConflict
	}
	if prior := op.DataRepair.Approval; prior != nil {
		if prior.Decision != in.Decision || prior.Actor != in.Actor || prior.Comment != in.Comment {
			return api.OperationEvent{}, false, ErrOperationApprovalDecided
		}
		return api.OperationEvent{}, true, nil
	}
	if op.DataRepair.Phase != api.DataRepairAwaitingApproval {
		return api.OperationEvent{}, false, ErrOperationNotAwaitingApproval
	}
	if !now.Before(op.DataRepair.PlanExpiresAt) {
		return api.OperationEvent{}, false, ErrWorkflowCallbackExpired
	}
	op.DataRepair.Approval = &api.OperationApprovalDecision{Step: "repair", Decision: in.Decision, Actor: in.Actor, Comment: in.Comment}
	op.DataRepair.PhaseStartedAt = now
	op.DataRepair.Phase, op.State = api.DataRepairApproved, api.OperationAccepted
	if in.Decision == api.OperationApprovalRejected {
		op.DataRepair.Phase, op.State = api.DataRepairRejected, api.OperationCancelled
	}
	op.Waiting = nil
	return dataRepairEvent(op, "approval_decided", now), false, nil
}
