package runtimeupgrade

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ReserveAndStage atomically freezes candidate inputs before storage I/O. A
// retry uses the same intent and path; only a verified handoff becomes runnable.
func (s Stager) ReserveAndStage(ctx context.Context, r state.RuntimeUpgradeOperationRequest) (state.RuntimeUpgradeOperation, error) {
	if err := ctx.Err(); err != nil {
		return state.RuntimeUpgradeOperation{}, err
	}
	store, ok := s.Store.(state.RuntimeUpgradeReservationStore)
	if !ok || !validStagingRequest(r) {
		return state.RuntimeUpgradeOperation{}, state.ErrInvalidArgument
	}
	path, err := s.CandidatePath(r.ID)
	if err != nil {
		return state.RuntimeUpgradeOperation{}, err
	}
	op, err := store.ReserveRuntimeUpgradeOperation(ctx, r, path)
	if err != nil {
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("reserve runtime upgrade source: %w", err)
	}
	if op.Phase != state.RuntimeUpgradeReserved {
		return op, nil
	}
	serving, err := s.stagingInputs(ctx, r)
	if err != nil {
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("reserved runtime upgrade staging inputs: %w", err)
	}
	if err := s.stageSource(ctx, r.ID, serving); err != nil {
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("stage reserved runtime upgrade source: %w", err)
	}
	op, err = store.PrepareReservedRuntimeUpgradeOperation(ctx, r.AccountID, r.ID)
	if err != nil {
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("prepare verified runtime upgrade source: %w", err)
	}
	return op, nil
}

// Status is deliberately separate from the worker journal: no lease tokens,
// spool paths, secret envelopes, database diagnostics or configuration values.
type Status struct {
	ID              string                             `json:"id"`
	AppID           string                             `json:"app_id"`
	CandidateID     string                             `json:"candidate_id"`
	ServingID       string                             `json:"serving_id"`
	TargetReleaseID string                             `json:"target_release_id"`
	Phase           state.RuntimeUpgradeOperationPhase `json:"phase"`
	Blocker         string                             `json:"blocker,omitempty"`
	CreatedAt       time.Time                          `json:"created_at"`
	DeadlineAt      time.Time                          `json:"deadline_at"`
	FinishedAt      time.Time                          `json:"finished_at,omitzero"`
	CanCancel       bool                               `json:"can_cancel"`
}

// Controls is a private account-scoped seam; it registers no public routes.
type Controls struct {
	Store state.RuntimeUpgradeReservationStore
}

func (c Controls) Status(ctx context.Context, accountID, id string) (Status, error) {
	if c.Store == nil {
		return Status{}, state.ErrInvalidArgument
	}
	op, err := c.Store.RuntimeUpgradeOperation(ctx, accountID, id)
	if err != nil {
		return Status{}, fmt.Errorf("read runtime upgrade status: %w", err)
	}
	return operationStatus(op), nil
}

func (c Controls) Cancel(ctx context.Context, accountID, id string) (Status, error) {
	if c.Store == nil {
		return Status{}, state.ErrInvalidArgument
	}
	op, err := c.Store.CancelRuntimeUpgradeOperation(ctx, accountID, id)
	if err != nil {
		return Status{}, fmt.Errorf("cancel runtime upgrade: %w", err)
	}
	return operationStatus(op), nil
}

func operationStatus(op state.RuntimeUpgradeOperation) Status {
	return Status{ID: op.ID, AppID: op.AppID, CandidateID: op.DeploymentID, ServingID: op.ServingDeploymentID, TargetReleaseID: op.TargetReleaseID,
		Phase: op.Phase, Blocker: op.Blocker, CreatedAt: op.CreatedAt, DeadlineAt: op.DeadlineAt, FinishedAt: op.FinishedAt,
		CanCancel: op.Phase == state.RuntimeUpgradeReserved || op.Phase == state.RuntimeUpgradePrepared || op.Phase == state.RuntimeUpgradeWaiting}
}

// Verify leaves activation history intact and evaluates current scoped evidence
// for an explicitly reviewed gateway process set. It registers no HTTP route.
func (c Controls) Verify(ctx context.Context, accountID, id string, sessions []string) (state.RuntimeUpgradeVerification, error) {
	store, ok := c.Store.(state.RuntimeUpgradeVerificationStore)
	if !ok {
		return state.RuntimeUpgradeVerification{}, state.ErrInvalidArgument
	}
	result, err := store.VerifyRuntimeUpgrade(ctx, accountID, id, sessions)
	if err != nil {
		return state.RuntimeUpgradeVerification{}, fmt.Errorf("verify runtime upgrade: %w", err)
	}
	return result, nil
}
