package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// MCPTaskPromotionRecovery retains the original activation evidence. Generation
// fences completion by an expired claimant; leases permit recovery after crash.
type MCPTaskPromotionRecovery struct {
	DeploymentID  string
	AppID         string
	NodeID        string
	Channel       string
	Payload       json.RawMessage
	Attempts      int
	Generation    int64
	HeldAt        time.Time
	nextAttemptAt time.Time
	leaseUntil    time.Time
	wakeRequested bool
}

func validMCPTaskPromotionRecovery(id string, recovery MCPTaskPromotionRecovery) bool {
	if recovery.Channel != "snapshot_written" && recovery.Channel != "deployment_ready" {
		return false
	}
	if len(recovery.Payload) == 0 || len(recovery.Payload) > 65536 {
		return false
	}
	var evidence struct {
		DeploymentID string `json:"deployment_id"`
	}
	return json.Unmarshal(recovery.Payload, &evidence) == nil && evidence.DeploymentID == id
}

func mcpTaskRecoveryBackoff(attempts int) time.Duration {
	// Bound the exponent before shifting so a long hold cannot overflow.
	if attempts > 7 {
		attempts = 7
	}
	if attempts < 1 {
		attempts = 1
	}
	delay := 5 * time.Second * time.Duration(1<<(attempts-1))
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func (s *PgStore) ClaimMCPTaskPromotionRecovery(ctx context.Context, nodeID string) (*MCPTaskPromotionRecovery, error) {
	// Reconcile a crash between activation/cancellation and queue completion.
	// An active claimant owns completion until its lease expires.
	if _, err := s.pool.Exec(ctx, `WITH finished AS (
        UPDATE deployment_mcp_task_holds h SET recovery_payload=NULL,lease_until=NULL,wake_requested=false,
        last_recovery_outcome=CASE WHEN d.status='live' THEN 'recovered' WHEN d.status='cancelled' THEN 'cancelled' ELSE 'inactive' END,
        last_recovery_at=clock_timestamp(),last_recovery_error_code=''
        FROM deployments d JOIN apps a ON a.id=d.app_id
        WHERE h.deployment_id=d.id AND h.recovery_payload IS NOT NULL
        AND (h.lease_until IS NULL OR h.lease_until <= clock_timestamp())
        AND (d.deleted_at IS NOT NULL OR a.status='deleted' OR d.status IN ('live','failed','cancelled','superseded'))
        RETURNING h.deployment_id,a.account_id,h.attempts,h.last_recovery_outcome
    ) INSERT INTO deployment_audit (deployment_id,account_id,kind,actor,at,data)
        SELECT deployment_id,account_id,CASE WHEN last_recovery_outcome='recovered' THEN 'deploy.mcp_task_recovered'
        WHEN last_recovery_outcome='cancelled' THEN 'deploy.mcp_task_hold_cancelled' ELSE 'deploy.mcp_task_retry_completed' END,
        'imaged:mcp_task_recovery',clock_timestamp(),jsonb_build_object('outcome',last_recovery_outcome,'attempt',attempts,'reconciled',true)
        FROM finished`); err != nil {
		return nil, err
	}
	var work MCPTaskPromotionRecovery
	err := s.pool.QueryRow(ctx, `WITH due AS (
        SELECT h.deployment_id FROM deployment_mcp_task_holds h JOIN deployments d ON d.id=h.deployment_id JOIN apps a ON a.id=d.app_id
        WHERE h.recovery_payload IS NOT NULL AND d.status IN ('pending','building','imaging','snapshotting')
        AND d.deleted_at IS NULL AND a.status <> 'deleted' AND (h.owner_node='' OR h.owner_node=$1)
        AND h.next_attempt_at <= clock_timestamp() AND (h.lease_until IS NULL OR h.lease_until <= clock_timestamp())
        ORDER BY h.next_attempt_at,h.deployment_id LIMIT 1 FOR UPDATE OF h SKIP LOCKED
    ), claimed AS (
        UPDATE deployment_mcp_task_holds h SET lease_until=clock_timestamp()+interval '2 minutes',
        claim_generation=h.claim_generation+1,wake_requested=false,attempts=LEAST(h.attempts+1,1000000)
        FROM due,deployments d WHERE h.deployment_id=due.deployment_id AND d.id=h.deployment_id
        RETURNING h.deployment_id,d.app_id,h.owner_node,h.recovery_channel,h.recovery_payload,h.attempts,h.claim_generation,h.first_held_at
    ), audited AS (
        INSERT INTO deployment_audit (deployment_id,account_id,kind,actor,at,data)
        SELECT c.deployment_id,a.account_id,'deploy.mcp_task_retry_started','imaged:mcp_task_recovery',clock_timestamp(),
        jsonb_build_object('attempt',c.attempts,'generation',c.claim_generation)
        FROM claimed c JOIN apps a ON a.id=c.app_id RETURNING id
    ) SELECT deployment_id::text,app_id::text,owner_node,recovery_channel,recovery_payload,attempts,claim_generation,first_held_at
        FROM claimed`, nodeID).Scan(&work.DeploymentID, &work.AppID, &work.NodeID, &work.Channel, &work.Payload, &work.Attempts, &work.Generation, &work.HeldAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &work, nil
}

func (s *PgStore) FinishMCPTaskPromotionRecovery(ctx context.Context, id string, generation int64, outcome string) (bool, error) {
	complete, kind, code, err := recoveryOutcome(outcome)
	if err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var attempts int
	err = tx.QueryRow(ctx, `UPDATE deployment_mcp_task_holds SET recovery_payload=CASE WHEN $3 THEN NULL ELSE recovery_payload END,
        next_attempt_at=clock_timestamp()+make_interval(secs => CASE WHEN wake_requested THEN 5 ELSE LEAST(300,5*power(2,LEAST(GREATEST(attempts-1,0),6))) END),
        lease_until=NULL,wake_requested=false,last_recovery_outcome=$4,last_recovery_at=clock_timestamp(),last_recovery_error_code=$5
        WHERE deployment_id=$1::uuid AND claim_generation=$2 AND lease_until > clock_timestamp()
        RETURNING attempts`, id, generation, complete, outcome, code).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := appendMCPTaskAuditTx(ctx, tx, id, kind, "imaged:mcp_task_recovery", map[string]any{"attempt": attempts, "generation": generation, "outcome": outcome, "error_code": code}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (m *MemStore) ClaimMCPTaskPromotionRecovery(ctx context.Context, nodeID string) (*MCPTaskPromotionRecovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	ids := make([]string, 0, len(m.mcpTaskPromotionHolds))
	for id, hold := range m.mcpTaskPromotionHolds {
		d, exists := m.deployments[id]
		app, appExists := m.apps[d.AppID]
		if !exists || !appExists || app.Status == AppDeleted || d.DeletedAt != nil || d.Status.IsTerminal() || d.Status == DeployLive {
			if hold.recovery.leaseUntil.After(now) {
				continue
			}
			if len(hold.recovery.Payload) > 0 {
				outcome, kind := "inactive", DeployMCPTaskRetryCompleted
				if d.Status == DeployLive {
					outcome, kind = "recovered", DeployMCPTaskRecovered
				} else if d.Status == DeployCancelled {
					outcome, kind = "cancelled", DeployMCPTaskHoldCancelled
				}
				if exists && appExists {
					if err := m.appendMCPTaskAuditLocked(id, kind, "imaged:mcp_task_recovery", map[string]any{"outcome": outcome, "attempt": hold.recovery.Attempts, "reconciled": true}); err != nil {
						return nil, err
					}
				}
				hold.LastRecoveryOutcome, hold.LastRecoveryAt, hold.LastRecoveryErrorCode = outcome, &now, ""
			}
			hold.recovery.Payload = nil
			hold.recovery.leaseUntil = time.Time{}
			hold.recovery.wakeRequested = false
			m.mcpTaskPromotionHolds[id] = hold
			continue
		}
		w := hold.recovery
		if len(w.Payload) > 0 && (w.NodeID == "" || w.NodeID == nodeID) && !w.nextAttemptAt.After(now) && !w.leaseUntil.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.mcpTaskPromotionHolds[ids[i]].recovery.nextAttemptAt, m.mcpTaskPromotionHolds[ids[j]].recovery.nextAttemptAt
		if a.Equal(b) {
			return ids[i] < ids[j]
		}
		return a.Before(b)
	})
	if len(ids) == 0 {
		return nil, nil
	}
	id := ids[0]
	hold := m.mcpTaskPromotionHolds[id]
	hold.recovery.Generation++
	hold.recovery.wakeRequested = false
	if hold.recovery.Attempts < 1000000 {
		hold.recovery.Attempts++
	}
	hold.recovery.leaseUntil = now.Add(2 * time.Minute)
	if err := m.appendMCPTaskAuditLocked(id, DeployMCPTaskRetryStarted, "imaged:mcp_task_recovery", map[string]any{"attempt": hold.recovery.Attempts, "generation": hold.recovery.Generation}); err != nil {
		return nil, err
	}
	m.mcpTaskPromotionHolds[id] = hold
	work := hold.recovery
	work.Payload = append([]byte(nil), work.Payload...)
	return &work, nil
}

func (m *MemStore) FinishMCPTaskPromotionRecovery(ctx context.Context, id string, generation int64, outcome string) (bool, error) {
	complete, kind, code, err := recoveryOutcome(outcome)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	hold, exists := m.mcpTaskPromotionHolds[id]
	now := time.Now().UTC()
	if !exists || hold.recovery.Generation != generation || !hold.recovery.leaseUntil.After(now) {
		return false, nil
	}
	if complete {
		hold.recovery.Payload = nil
	}
	delay := mcpTaskRecoveryBackoff(hold.recovery.Attempts)
	if hold.recovery.wakeRequested {
		delay = 5 * time.Second
	}
	hold.recovery.wakeRequested = false
	hold.recovery.nextAttemptAt = now.Add(delay)
	hold.recovery.leaseUntil = time.Time{}
	hold.LastRecoveryOutcome, hold.LastRecoveryAt, hold.LastRecoveryErrorCode = outcome, &now, code
	if err := m.appendMCPTaskAuditLocked(id, kind, "imaged:mcp_task_recovery", map[string]any{"attempt": hold.recovery.Attempts, "generation": generation, "outcome": outcome, "error_code": code}); err != nil {
		return false, err
	}
	m.mcpTaskPromotionHolds[id] = hold
	return true, nil
}
