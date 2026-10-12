package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func appendMCPTaskAuditTx(ctx context.Context, tx pgx.Tx, id string, kind DeploymentAuditKind, actor string, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO deployment_audit (deployment_id,account_id,kind,actor,at,data)
        SELECT d.id,a.account_id,$2,$3,clock_timestamp(),$4::jsonb
        FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=$1::uuid`, id, string(kind), actor, raw)
	return err
}

func (m *MemStore) appendMCPTaskAuditLocked(id string, kind DeploymentAuditKind, actor string, data map[string]any) error {
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	depID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	accountID, err := uuid.Parse(m.apps[d.AppID].AccountID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = m.appendDeploymentAuditLocked(DeploymentAudit{DeploymentID: depID, AccountID: &accountID, Kind: kind, Actor: actor, Data: raw, At: time.Now().UTC()})
	return err
}

func recoveryOutcome(outcome string) (bool, DeploymentAuditKind, string, error) {
	switch outcome {
	case "recovered":
		return true, DeployMCPTaskRecovered, "", nil
	case "inactive":
		return true, DeployMCPTaskRetryCompleted, "", nil
	case "held":
		return false, DeployMCPTaskRetryCompleted, "mcp_task_compatibility_gate_blocked", nil
	case "retry":
		return false, DeployMCPTaskRetryCompleted, "mcp_task_recovery_attempt_failed", nil
	default:
		return false, "", "", ErrInvalidArgument
	}
}

// RetryMCPTaskPromotionRecovery only changes scheduling. Evidence, ownership,
// leases and activation checks remain authoritative. Repeated requests for
// already due work or an already requested follow-up have no additional effect.
func (s *PgStore) RetryMCPTaskPromotionRecovery(ctx context.Context, id, actor string) error {
	if actor == "" || len(actor) > 512 {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status DeploymentStatus
	var deleted bool
	err = tx.QueryRow(ctx, `SELECT d.status,(d.deleted_at IS NOT NULL OR a.status='deleted')
        FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=$1::uuid FOR UPDATE OF d`, id).Scan(&status, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if deleted || !status.IsCancelEligible() {
		return ErrInvalidStateTransition
	}
	var available, alreadyDue bool
	err = tx.QueryRow(ctx, `SELECT recovery_payload IS NOT NULL,
        CASE WHEN lease_until > clock_timestamp() THEN wake_requested ELSE next_attempt_at <= clock_timestamp() END
        FROM deployment_mcp_task_holds WHERE deployment_id=$1::uuid FOR UPDATE`, id).Scan(&available, &alreadyDue)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidStateTransition
	}
	if err != nil {
		return err
	}
	if !available {
		return ErrInvalidStateTransition
	}
	if alreadyDue {
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `UPDATE deployment_mcp_task_holds SET next_attempt_at=clock_timestamp(),
        wake_requested=COALESCE(lease_until > clock_timestamp(),false) WHERE deployment_id=$1::uuid`, id)
	if err != nil {
		return err
	}
	if err := appendMCPTaskAuditTx(ctx, tx, id, DeployMCPTaskRetryRequested, actor, map[string]any{"trigger": "operator"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *MemStore) RetryMCPTaskPromotionRecovery(ctx context.Context, id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if actor == "" || len(actor) > 512 {
		return ErrInvalidArgument
	}
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if !d.Status.IsCancelEligible() || d.DeletedAt != nil || m.apps[d.AppID].Status == AppDeleted {
		return ErrInvalidStateTransition
	}
	hold, ok := m.mcpTaskPromotionHolds[id]
	if !ok || len(hold.recovery.Payload) == 0 {
		return ErrInvalidStateTransition
	}
	now := time.Now().UTC()
	leased := hold.recovery.leaseUntil.After(now)
	if leased && hold.recovery.wakeRequested || !leased && !hold.recovery.nextAttemptAt.After(now) {
		return nil
	}
	if err := m.appendMCPTaskAuditLocked(id, DeployMCPTaskRetryRequested, actor, map[string]any{"trigger": "operator"}); err != nil {
		return err
	}
	hold.recovery.nextAttemptAt = now
	hold.recovery.wakeRequested = leased
	m.mcpTaskPromotionHolds[id] = hold
	return nil
}

// MCPTaskPromotionHoldAgeSeconds is a current state metric. It returns zero
// when no active candidate is held and never depends on pushed app metrics.
func (s *PgStore) MCPTaskPromotionHoldAgeSeconds(ctx context.Context, accountID, appID string, at time.Time) (float64, error) {
	var age float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(GREATEST(0,EXTRACT(EPOCH FROM ($3::timestamptz-h.first_held_at)))),0)::double precision
        FROM deployment_mcp_task_holds h JOIN deployments d ON d.id=h.deployment_id JOIN apps a ON a.id=d.app_id
        WHERE a.account_id=$1::uuid AND (NULLIF($2,'')::uuid IS NULL OR d.app_id=NULLIF($2,'')::uuid)
        AND a.status <> 'deleted' AND d.deleted_at IS NULL AND d.status IN ('pending','building','imaging','snapshotting')`, accountID, appID, at).Scan(&age)
	return age, err
}

func (m *MemStore) MCPTaskPromotionHoldAgeSeconds(ctx context.Context, accountID, appID string, at time.Time) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var age float64
	for id, hold := range m.mcpTaskPromotionHolds {
		d, ok := m.deployments[id]
		app, exists := m.apps[d.AppID]
		if !ok || !exists || app.AccountID != accountID || app.Status == AppDeleted || d.DeletedAt != nil || !d.Status.IsCancelEligible() || appID != "" && d.AppID != appID {
			continue
		}
		if seconds := at.Sub(hold.recovery.HeldAt).Seconds(); seconds > age {
			age = seconds
		}
	}
	return age, nil
}

// cancelMCPTaskHoldTx shares the deployment cancellation transaction; it emits
// one cancellation event and immediately removes pending recovery eligibility.
func cancelMCPTaskHoldTx(ctx context.Context, tx pgx.Tx, id, actor string) error {
	result, err := tx.Exec(ctx, `UPDATE deployment_mcp_task_holds SET recovery_payload=NULL,lease_until=NULL,wake_requested=false,
        last_recovery_outcome='cancelled',last_recovery_at=clock_timestamp(),last_recovery_error_code=''
        WHERE deployment_id=$1::uuid AND last_recovery_outcome <> 'cancelled'`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	return appendMCPTaskAuditTx(ctx, tx, id, DeployMCPTaskHoldCancelled, actor, map[string]any{"outcome": "cancelled"})
}
