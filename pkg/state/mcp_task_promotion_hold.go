package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// MCPTaskPromotionHold records the latest blocked automatic promotion. Reads
// fence it against the current lifecycle so completed/cancelled candidates do
// not continue to display a hold, even if a delayed retry writes the record.
type MCPTaskPromotionHold struct {
	Detail                string     `json:"detail"`
	ObservedAt            time.Time  `json:"observed_at"`
	HeldAt                time.Time  `json:"held_at"`
	RecoveryAttempts      int        `json:"recovery_attempts"`
	NextRetryAt           *time.Time `json:"next_retry_at,omitempty"`
	RecoveryInProgress    bool       `json:"recovery_in_progress"`
	LastRecoveryOutcome   string     `json:"last_recovery_outcome,omitempty"`
	LastRecoveryAt        *time.Time `json:"last_recovery_at,omitempty"`
	LastRecoveryErrorCode string     `json:"last_recovery_error_code,omitempty"`
	recovery              MCPTaskPromotionRecovery
}

func (s *PgStore) RecordMCPTaskPromotionHold(ctx context.Context, id, detail string, recovery MCPTaskPromotionRecovery) error {
	if len(detail) > 8192 || !validMCPTaskPromotionRecovery(id, recovery) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status DeploymentStatus
	err = tx.QueryRow(ctx, `SELECT status FROM deployments WHERE id=$1::uuid FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !status.IsCancelEligible() {
		return nil
	}
	var first bool
	if err := tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM deployment_mcp_task_holds WHERE deployment_id=$1::uuid)`, id).Scan(&first); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO deployment_mcp_task_holds (deployment_id,detail,recovery_channel,recovery_payload,owner_node)
        VALUES ($1::uuid,$2,$3,$4::jsonb,$5)
        ON CONFLICT (deployment_id) DO UPDATE SET detail=EXCLUDED.detail,observed_at=clock_timestamp(),
        recovery_channel=EXCLUDED.recovery_channel,recovery_payload=EXCLUDED.recovery_payload,owner_node=EXCLUDED.owner_node`, id, detail, recovery.Channel, recovery.Payload, recovery.NodeID)
	if err != nil {
		return err
	}
	if first {
		if err := appendMCPTaskAuditTx(ctx, tx, id, DeployMCPTaskHeld, "imaged:mcp_task_recovery", map[string]any{"reason_code": "mcp_task_compatibility_gate_blocked"}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) MCPTaskPromotionHold(ctx context.Context, id string) (*MCPTaskPromotionHold, error) {
	var hold MCPTaskPromotionHold
	err := s.pool.QueryRow(ctx, `SELECT h.detail,h.observed_at,h.first_held_at,h.attempts,
        CASE WHEN h.recovery_payload IS NOT NULL THEN GREATEST(h.next_attempt_at,COALESCE(h.lease_until,h.next_attempt_at)) END,
        COALESCE(h.lease_until > clock_timestamp(),false),h.last_recovery_outcome,h.last_recovery_at,h.last_recovery_error_code
        FROM deployment_mcp_task_holds h JOIN deployments d ON d.id = h.deployment_id
        WHERE d.id = $1::uuid AND d.status IN ('pending', 'building', 'imaging', 'snapshotting')`, id).Scan(&hold.Detail, &hold.ObservedAt, &hold.HeldAt, &hold.RecoveryAttempts, &hold.NextRetryAt, &hold.RecoveryInProgress, &hold.LastRecoveryOutcome, &hold.LastRecoveryAt, &hold.LastRecoveryErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &hold, nil
}

func (m *MemStore) RecordMCPTaskPromotionHold(ctx context.Context, id, detail string, recovery MCPTaskPromotionRecovery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(detail) > 8192 || !validMCPTaskPromotionRecovery(id, recovery) {
		return ErrInvalidArgument
	}
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if d.Status.IsTerminal() || d.Status == DeployLive {
		return nil
	}
	if m.mcpTaskPromotionHolds == nil {
		m.mcpTaskPromotionHolds = make(map[string]MCPTaskPromotionHold)
	}
	now := time.Now().UTC()
	old, exists := m.mcpTaskPromotionHolds[id]
	recovery.DeploymentID = id
	recovery.AppID = d.AppID
	recovery.Payload = append([]byte(nil), recovery.Payload...)
	if exists {
		recovery.Attempts, recovery.Generation = old.recovery.Attempts, old.recovery.Generation
		recovery.HeldAt, recovery.nextAttemptAt, recovery.leaseUntil = old.recovery.HeldAt, old.recovery.nextAttemptAt, old.recovery.leaseUntil
		recovery.wakeRequested = old.recovery.wakeRequested
	} else {
		recovery.HeldAt, recovery.nextAttemptAt = now, now
	}
	if !exists {
		if err := m.appendMCPTaskAuditLocked(id, DeployMCPTaskHeld, "imaged:mcp_task_recovery", map[string]any{"reason_code": "mcp_task_compatibility_gate_blocked"}); err != nil {
			return err
		}
	}
	old.Detail, old.ObservedAt, old.recovery = detail, now, recovery
	m.mcpTaskPromotionHolds[id] = old
	return nil
}

func (m *MemStore) MCPTaskPromotionHold(ctx context.Context, id string) (*MCPTaskPromotionHold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d, ok := m.deployments[id]
	if !ok {
		return nil, ErrNotFound
	}
	hold, held := m.mcpTaskPromotionHolds[id]
	if (d.Status.IsTerminal() || d.Status == DeployLive) || !held {
		return nil, nil
	}
	hold.HeldAt = hold.recovery.HeldAt
	hold.RecoveryAttempts = hold.recovery.Attempts
	hold.RecoveryInProgress = hold.recovery.leaseUntil.After(time.Now().UTC())
	if len(hold.recovery.Payload) > 0 {
		next := hold.recovery.nextAttemptAt
		if hold.recovery.leaseUntil.After(next) {
			next = hold.recovery.leaseUntil
		}
		hold.NextRetryAt = &next
	}
	if hold.LastRecoveryAt != nil {
		at := *hold.LastRecoveryAt
		hold.LastRecoveryAt = &at
	}
	return &hold, nil
}
