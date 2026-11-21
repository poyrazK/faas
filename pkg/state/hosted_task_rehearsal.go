package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type HostedTaskRehearsalAction string

const (
	PrepareHostedTaskRehearsal HostedTaskRehearsalAction = "prepare"
	ReadHostedTaskRehearsal    HostedTaskRehearsalAction = "read"
	DecideHostedTaskRehearsal  HostedTaskRehearsalAction = "decide"
	ClaimHostedTaskRehearsal   HostedTaskRehearsalAction = "claim"
)

type HostedTaskRehearsalStore interface {
	ChangeHostedTaskRehearsal(context.Context, string, string, HostedTaskRehearsalAction, HostedTaskRehearsalChange) (HostedTaskRehearsalResult, error)
}

// OwnerKeyID is set by the authenticated caller, never decoded from the request.
type HostedTaskRehearsalChange struct {
	OwnerKeyID string
	Prepare    api.HostedTaskRehearsalPrepare
	Decision   api.HostedTaskRehearsalDecision
	PlanSHA256 string // claim only
}

type HostedTaskRehearsalResult struct {
	Plan          api.HostedTaskRehearsalPlan
	ClaimAcquired bool                    // private, returned only once, never serialized by the API
	Run           *HostedTaskRehearsalRun // private reservation; a replay can read it
	ReadyToClaim  bool                    // private; the supplied owner holds this unconsumed approval
}

// HostedTaskRehearsalRun is a reserved identity for the exact approved plan.
type HostedTaskRehearsalRun struct {
	ID                string
	PlanSHA256        string
	ReservedAt        time.Time
	ApprovalExpiresAt time.Time
}

func rehearsalRunForClaim(plan api.HostedTaskRehearsalPlan) HostedTaskRehearsalRun {
	return HostedTaskRehearsalRun{
		ID: plan.TargetID, PlanSHA256: plan.PlanSHA256,
		ReservedAt: plan.ClaimedAt.UTC(), ApprovalExpiresAt: plan.ExpiresAt.UTC(),
	}
}

type hostedTaskRehearsalRecord = tasktoolrehearsal.Record

func validHostedTaskRehearsalChange(account, id string, action HostedTaskRehearsalAction, in HostedTaskRehearsalChange) bool {
	if uuid.Validate(account) != nil || !toolpublication.ValidID(id) || uuid.Validate(in.OwnerKeyID) != nil {
		return false
	}
	switch action {
	case PrepareHostedTaskRehearsal:
		return tasktoolrehearsal.ValidPrepare(in.Prepare)
	case ReadHostedTaskRehearsal:
		return true
	case DecideHostedTaskRehearsal:
		return toolpublication.ValidID(in.Decision.PlanSHA256) && (in.Decision.Decision == "approve" || in.Decision.Decision == "revoke")
	case ClaimHostedTaskRehearsal:
		return toolpublication.ValidID(in.PlanSHA256)
	}
	return false
}

func applyHostedTaskRehearsal(r hostedTaskRehearsalRecord, exists bool, binding api.HostedTaskBindingVerification, approvedByFresh bool, action HostedTaskRehearsalAction, in HostedTaskRehearsalChange, now time.Time) (hostedTaskRehearsalRecord, bool, error) {
	if !exists {
		if action != PrepareHostedTaskRehearsal {
			return r, false, ErrNotFound
		}
		out, err := tasktoolrehearsal.New(binding, in.Prepare, now)
		return out, false, hostedTaskRehearsalError(err)
	}
	r = tasktoolrehearsal.Refresh(r, binding, approvedByFresh, now)
	switch action {
	case PrepareHostedTaskRehearsal:
		if r.Plan.HostedTaskRehearsalPrepare != in.Prepare {
			return r, false, ErrConflict
		}
	case DecideHostedTaskRehearsal:
		out, err := tasktoolrehearsal.Decide(r, in.Decision, in.OwnerKeyID, now)
		return out, false, hostedTaskRehearsalError(err)
	case ClaimHostedTaskRehearsal:
		out, acquired, err := tasktoolrehearsal.Claim(r, in.PlanSHA256, in.OwnerKeyID, now)
		return out, acquired, hostedTaskRehearsalError(err)
	}
	return r, false, nil
}

func hostedTaskRehearsalError(err error) error {
	switch {
	case errors.Is(err, tasktoolrehearsal.ErrInvalid):
		return ErrInvalidArgument
	case errors.Is(err, tasktoolrehearsal.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}

func (m *MemStore) ChangeHostedTaskRehearsal(ctx context.Context, account, id string, action HostedTaskRehearsalAction, in HostedTaskRehearsalChange) (HostedTaskRehearsalResult, error) {
	var zero HostedTaskRehearsalResult
	if !validHostedTaskRehearsalChange(account, id, action, in) {
		return zero, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	owner := HostedTaskSourceOwner{KeyID: in.OwnerKeyID}
	if !m.hostedTaskSourceOwnerFreshLocked(account, owner) {
		return zero, ErrNotFound
	}
	binding, err := m.changeHostedTaskBindingsLocked(ctx, account, id, "get", HostedTaskBindingChange{})
	if err != nil {
		return zero, err
	}
	key := account + ":" + id
	record, exists := m.hostedTaskRehearsals[key]
	if exists && tasktoolrehearsal.ValidateRecord(record, account, id) != nil {
		return zero, ErrInvalidArgument
	}
	approvalFresh := record.ApprovalKeyID == "" || m.hostedTaskSourceOwnerFreshLocked(account, HostedTaskSourceOwner{KeyID: record.ApprovalKeyID})
	now := time.Now().UTC().Truncate(time.Microsecond)
	record, acquired, err := applyHostedTaskRehearsal(record, exists, binding.Public, approvalFresh, action, in, now)
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !m.hostedTaskSourceOwnerFreshLocked(account, owner) {
		return zero, ErrNotFound
	}
	if tasktoolrehearsal.ValidateRecord(record, account, id) != nil {
		return zero, ErrConflict
	}
	if m.hostedTaskRehearsals == nil {
		m.hostedTaskRehearsals = make(map[string]hostedTaskRehearsalRecord)
	}
	var run *HostedTaskRehearsalRun
	if action == ClaimHostedTaskRehearsal && record.Plan.Status == tasktoolrehearsal.Claimed {
		reservation, exists := m.hostedTaskRehearsalRuns[key]
		expected := rehearsalRunForClaim(record.Plan)
		if acquired {
			if exists || !time.Now().Before(record.Plan.ExpiresAt) {
				return zero, ErrConflict
			}
			if m.hostedTaskRehearsalRuns == nil {
				m.hostedTaskRehearsalRuns = make(map[string]HostedTaskRehearsalRun)
			}
			m.hostedTaskRehearsalRuns[key] = expected
			run = &expected
		} else if exists {
			if reservation != expected {
				return zero, ErrConflict
			}
			run = &reservation
		}
	}
	m.hostedTaskRehearsals[key] = record
	plan := record.Plan
	if plan.ApprovedAt != nil {
		stamp := *plan.ApprovedAt
		plan.ApprovedAt = &stamp
	}
	if plan.ClaimedAt != nil {
		stamp := *plan.ClaimedAt
		plan.ClaimedAt = &stamp
	}
	return HostedTaskRehearsalResult{
		Plan: plan, ClaimAcquired: acquired, Run: run,
		ReadyToClaim: plan.Status == tasktoolrehearsal.Approved && record.ApprovalKeyID == in.OwnerKeyID,
	}, nil
}
