package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrApplicationStandardOperationStale = errors.New("state: application standard operation changed")

type ApplicationStandardOperationAction string

const (
	ApplicationStandardOperationPause  ApplicationStandardOperationAction = "pause"
	ApplicationStandardOperationResume ApplicationStandardOperationAction = "resume"
	ApplicationStandardOperationAbort  ApplicationStandardOperationAction = "abort"
)

// Operator controls remain private until consumer and native acceptance pass.
// Aborting stops outstanding targets; reversing installed controls needs a new review.
type ApplicationStandardOperationControlStore interface {
	ControlApplicationStandardOperation(context.Context, string, string, string, time.Time, ApplicationStandardOperationAction) (ApplicationStandardOperation, error)
}

func validStandardOperationControl(orgID, actorID, operationID string, expected time.Time, action ApplicationStandardOperationAction) bool {
	validAction := action == ApplicationStandardOperationPause || action == ApplicationStandardOperationResume || action == ApplicationStandardOperationAbort
	return standardApprovalIdentityValid(orgID, actorID, operationID) && !expected.IsZero() && expected.Equal(expected.Truncate(time.Microsecond)) && validAction
}

func prepareStandardOperationControl(o ApplicationStandardOperation, expected time.Time, action ApplicationStandardOperationAction, now time.Time) (ApplicationStandardOperation, error) {
	if !o.UpdatedAt.Equal(expected) {
		return o, ErrApplicationStandardOperationStale
	}
	next, err := standardOperationControlState(o, action)
	if err != nil || next == o.State {
		return o, err
	}
	o = cloneStandardOperation(o)
	o.State, o.ErrorCode = next, ""
	now = now.UTC().Truncate(time.Microsecond)
	if !now.After(expected) {
		now = expected.Add(time.Microsecond)
	}
	o.UpdatedAt = now
	if action == ApplicationStandardOperationAbort {
		o.ErrorCode = "operator_aborted"
		for i := range o.Targets {
			if o.Targets[i].State == "queued" || o.Targets[i].State == "applying" {
				o.Targets[i].State, o.Targets[i].ErrorCode, o.Targets[i].UpdatedAt = "skipped", "operator_aborted", now
			}
		}
	}
	return o, nil
}

func standardOperationControlState(o ApplicationStandardOperation, action ApplicationStandardOperationAction) (string, error) {
	switch action {
	case ApplicationStandardOperationPause:
		if standardOperationActive(o.State) {
			return "paused", nil
		}
	case ApplicationStandardOperationResume:
		if o.State == "paused" {
			return standardProjectionOperationState(o), nil
		}
	case ApplicationStandardOperationAbort:
		if standardOperationActive(o.State) || o.State == "failed" && o.ErrorCode == "operator_aborted" {
			return "failed", nil
		}
	}
	return "", ErrConflict
}

func standardOperationControlAudit(before, after ApplicationStandardOperation, actorID string, action ApplicationStandardOperationAction) AuditLog {
	data, _ := json.Marshal(map[string]any{"org_id": after.OrgID, "operation_id": after.ID, "assignment_id": after.AssignmentID, "approval_hash": after.ApprovalHash, "actor_id": canonicalStandardUUID(actorID), "action": action, "previous_state": before.State, "state": after.State, "previous_updated_at": before.UpdatedAt, "updated_at": after.UpdatedAt})
	return AuditLog{ID: uuid.New(), Kind: "application_standard.operation_" + string(action), ReceivedAt: after.UpdatedAt, Data: data}
}
