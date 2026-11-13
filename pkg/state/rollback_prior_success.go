package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Outcome history remains writable after soft deletion. Take the lifecycle
// parent locks without applying the active-app eligibility check.
func lockRollbackOutcomeApp(ctx context.Context, tx pgx.Tx, appID string) error {
	q := sqlc.New()
	owner, err := q.ReadLifecycleAppOwner(ctx, tx, appID)
	if err != nil {
		return mapErr(err)
	}
	if _, err = q.LockRoutePolicyAccount(ctx, tx, sqlc.LockRoutePolicyAccountParams{AccountID: owner, AppID: appID}); err != nil {
		return mapErr(err)
	}
	locked, err := q.LockTrafficAppAccount(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return mapErr(err)
	}
	if pgUUIDString(locked) != owner {
		return ErrConflict
	}
	return nil
}

// The app/target lock is held before inspecting pending receipts. Preserve
// historical success before a new preparation clears rollout_completed_at.
func preserveRollbackSuccessTx(ctx context.Context, tx pgx.Tx, target Deployment) error {
	if target.RolloutCompletedAt == nil || target.Status != DeployLive && target.Status != DeploySuperseded {
		return nil
	}
	q := sqlc.New()
	rows, err := q.PendingRollbackSuccessesForTarget(ctx, tx, sqlc.PendingRollbackSuccessesForTargetParams{TargetID: mustPgUUID(target.ID), CompletedAt: pgTimestamptzUTC(*target.RolloutCompletedAt)})
	if err != nil {
		return err
	}
	for _, row := range rows {
		r := RollbackRecord{ID: row.ID, AccountID: row.AccountID, AppID: row.AppID, Scope: row.Scope, TargetDeploymentID: row.TargetDeploymentID, FromDeploymentID: row.FromID, Trigger: row.Trigger, RequestedAt: row.RequestedAt.Time}
		done, ok := RollbackCompletedAt(r.RequestedAt, target)
		if !ok {
			continue
		}
		audit, ms, err := priorRollbackSuccessAudit(r, done)
		if err != nil {
			return err
		}
		id, err := q.PreserveRollbackSuccessAudit(ctx, tx, sqlc.PreserveRollbackSuccessAuditParams{TargetID: mustPgUUID(r.TargetDeploymentID), AccountID: mustPgUUID(r.AccountID), Kind: string(audit.Kind), At: pgTimestamptzUTC(done), Data: audit.Data})
		if err != nil {
			return err
		}
		if err = q.PreserveRollbackSuccessReceipt(ctx, tx, sqlc.PreserveRollbackSuccessReceiptParams{ID: mustPgUUID(r.ID), At: pgTimestamptzUTC(done), RecoveryMs: ms, AuditID: id}); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) preserveRollbackSuccessLocked(target Deployment) error {
	for id, r := range m.rollbackRecords {
		if r.TargetDeploymentID != target.ID || r.Status != RollbackStatusPreparing {
			continue
		}
		done, ok := RollbackCompletedAt(r.RequestedAt, target)
		if !ok {
			continue
		}
		audit, ms, err := priorRollbackSuccessAudit(r, done)
		if err != nil {
			return err
		}
		auditID, err := m.appendDeploymentAuditLocked(audit)
		if err != nil {
			return err
		}
		r.Status, r.FinishedAt, r.RecoveryMS, r.AuditID = RollbackStatusComplete, &done, &ms, auditID
		r.ErrorCode, r.Error = "", ""
		m.rollbackRecords[id] = r
	}
	return nil
}

func priorRollbackSuccessAudit(r RollbackRecord, done time.Time) (DeploymentAudit, int64, error) {
	ms := max(int64(0), done.Sub(r.RequestedAt).Milliseconds())
	data, _ := json.Marshal(map[string]any{"rollback_id": r.ID, "from": r.FromDeploymentID, "to": r.TargetDeploymentID, "trigger": r.Trigger, "phase": "completed", "recovery_ms": ms})
	account, err := uuid.Parse(r.AccountID)
	if err != nil {
		return DeploymentAudit{}, 0, fmt.Errorf("state: prior rollback account: %w", err)
	}
	target, err := uuid.Parse(r.TargetDeploymentID)
	if err != nil {
		return DeploymentAudit{}, 0, fmt.Errorf("state: prior rollback target: %w", err)
	}
	return DeploymentAudit{DeploymentID: target, AccountID: &account, Kind: DeployRolledBack, Actor: "apid:rollback_outcome", At: done, Data: data}, ms, nil
}
