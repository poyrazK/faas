package state

import (
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func validDataRepairParent(p api.OperationDataRepair) bool {
	if p.ReplacesPlanID == "" {
		return p.ReplacesPlanDigest == ""
	}
	id, err := uuid.Parse(p.ReplacesPlanID)
	return err == nil && id != uuid.Nil && id.String() == p.ReplacesPlanID && p.ReplacesPlanID != p.PlanID &&
		p.ReplacesPlanID != p.SourceOperationID && repairHash.MatchString(p.ReplacesPlanDigest)
}

func reserveDataRepairReplacement(op *Operation, in DataRepairAdvance, now time.Time) (api.OperationEvent, error) {
	if op.DataRepair.Candidates < 1 {
		return api.OperationEvent{}, ErrConflict
	}
	id, err := uuid.Parse(in.ReplacementOperationID)
	if err != nil || id == uuid.Nil || id.String() != in.ReplacementOperationID || in.ReplacementOperationID == op.ID ||
		in.ReplacementOperationID == op.DataRepair.SourceOperationID || in.AppliedAt != nil || in.ApplyClosedAt != nil {
		return api.OperationEvent{}, ErrInvalidArgument
	}
	switch op.DataRepair.Phase {
	case api.DataRepairPrepared, api.DataRepairAwaitingApproval, api.DataRepairApproved, api.DataRepairFailed, api.DataRepairRejected, api.DataRepairClosed:
	default:
		// In particular, neither elapsed time nor an absent source receipt
		// releases an applying/unknown operation or an active rehearsal task.
		return api.OperationEvent{}, ErrConflict
	}
	if op.DataRepair.ReplacementOperationID != "" {
		return api.OperationEvent{}, ErrConflict
	}
	op.DataRepair.ReplacementOperationID = in.ReplacementOperationID
	op.DataRepair.Phase, op.DataRepair.PhaseStartedAt = api.DataRepairReplacing, now
	if !op.State.Terminal() {
		op.State, op.FailureCode = api.OperationRunning, ""
	}
	op.Waiting = nil
	return dataRepairEvent(op, "progress", now), nil
}

// finishDataRepairReplacement runs under the same store transaction/memory lock
// as child admission. No parent approval or intent is copied to the child.
func finishDataRepairReplacement(parent *Operation, in DataRepairAdmission, now time.Time) (api.OperationEvent, error) {
	p := parent.DataRepair
	if p == nil || parent.AccountID != in.AccountID || parent.AppID != in.AppID || parent.DataRepairBinding != in.Binding ||
		parent.ID != in.Plan.ReplacesPlanID || p.PlanDigest != in.Plan.ReplacesPlanDigest || p.SourceOperationID != in.Plan.SourceOperationID ||
		p.Phase != api.DataRepairReplacing || p.ReplacementOperationID != in.ID ||
		in.Plan.Candidates > p.Candidates || in.Plan.Candidates+in.Plan.Conflicts+in.Plan.Unavailable != p.Candidates+p.Conflicts+p.Unavailable {
		return api.OperationEvent{}, ErrConflict
	}
	p.Phase, p.PhaseStartedAt = api.DataRepairReplaced, now
	parent.State, parent.FailureCode, parent.Waiting = api.OperationCancelled, "repair_plan_replaced", nil
	return dataRepairEvent(parent, "cancelled", now), nil
}
