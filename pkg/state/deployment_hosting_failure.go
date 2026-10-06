package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	_ DeploymentHostingFailureStore = (*PgStore)(nil)
	_ DeploymentHostingFailureStore = (*MemStore)(nil)
)

func hostingFailureReceipt(deploymentID string, raw []byte) (apihostingreceipt.Receipt, error) {
	receipt, err := apihostingreceipt.Decode(raw)
	if err != nil {
		return receipt, fmt.Errorf("hosting failure receipt: %w", err)
	}
	if receipt.DeploymentID != deploymentID || receipt.Smoke.Status != apihostingreceipt.SmokeFailed {
		return receipt, fmt.Errorf("hosting failure receipt: %w", ErrInvalidArgument)
	}
	return receipt, nil
}

// FailDeploymentWithHostingReceipt preserves the existing failure transaction
// while including the verdict and fencing stale or duplicate verifiers.
func (s *PgStore) FailDeploymentWithHostingReceipt(ctx context.Context, id string, raw []byte, code, message string) (bool, error) {
	receipt, err := hostingFailureReceipt(id, raw)
	if err != nil {
		return false, err
	}
	deploymentID, err := parsePgUUID(id)
	if err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	locked, err := q.LockDeploymentHostingFailure(ctx, tx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("lock hosting failure: %w", err)
	}
	if uuidString(locked.AppID) != receipt.AppID {
		return false, ErrInvalidArgument
	}
	status := DeploymentStatus(locked.Status)
	if status.IsTerminal() || status == DeployLive {
		return false, nil
	}
	if status != DeploySnapshotting {
		return false, ErrInvalidStateTransition
	}
	rows, err := q.WriteDeploymentHostingFailureReceipt(ctx, tx, sqlc.WriteDeploymentHostingFailureReceiptParams{DeploymentID: deploymentID, Receipt: raw})
	if err != nil {
		return false, fmt.Errorf("write hosting failure receipt: %w", err)
	}
	if rows != 1 {
		return false, ErrInvalidStateTransition
	}
	if _, err := setDeploymentFailedTx(ctx, tx, id, code, message); err != nil {
		return false, fmt.Errorf("finalize hosting failure: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit hosting failure: %w", err)
	}
	return true, nil
}

// FailDeploymentWithHostingReceipt mirrors the PostgreSQL transaction under
// one mutex; validation and fallible activity work precede row mutation.
func (m *MemStore) FailDeploymentWithHostingReceipt(ctx context.Context, id string, raw []byte, code, message string) (bool, error) {
	receipt, err := hostingFailureReceipt(id, raw)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d, ok := m.deployments[id]
	if !ok {
		return false, ErrNotFound
	}
	if d.AppID != receipt.AppID {
		return false, ErrInvalidArgument
	}
	if d.Status.IsTerminal() || d.Status == DeployLive {
		return false, nil
	}
	if d.Status != DeploySnapshotting {
		return false, ErrInvalidStateTransition
	}
	if len(d.StageState) > 0 {
		var stages StageState
		if err := json.Unmarshal(d.StageState, &stages); err != nil {
			return false, fmt.Errorf("decode hosting failure stage: %w", err)
		}
	}
	if err := m.checkBindingReleaseFailureLocked(d); err != nil {
		return false, err
	}
	if err := m.enqueueDeploymentOutcomeActivityLocked(id, "failed", code); err != nil {
		return false, err
	}
	d.APIHostingReceipt = append([]byte(nil), raw...)
	d.ErrorCode = code
	m.failDeploymentLocked(d, message)
	m.markDeploymentSnapshotsStaleLocked(id)
	return true, nil
}
