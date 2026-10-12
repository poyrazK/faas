package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ReconcileObsoleteMCPTaskHolds runs independently of observer evidence and
// retry scheduling. A newer build alone is insufficient: it must serve traffic.
// Reviewed environment candidates and checked rollback targets keep their
// coordinator-owned lifecycle. Artifacts retain the normal supersession window.
func (s *PgStore) ReconcileObsoleteMCPTaskHolds(ctx context.Context, node string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id::text FROM deployments d
        JOIN deployment_mcp_task_holds h ON h.deployment_id=d.id
        WHERE d.status IN ('pending','building','imaging','snapshotting') AND d.deleted_at IS NULL
        AND d.environment_workload_runtime IS NULL AND (h.owner_node='' OR h.owner_node=$1)
        AND EXISTS (SELECT 1 FROM deployments n WHERE n.app_id=d.app_id AND n.scope=d.scope
            AND n.revision>d.revision AND n.status='live' AND n.traffic_percent>0 AND n.deleted_at IS NULL)
        AND NOT EXISTS (SELECT 1 FROM deployment_rollback_operations r WHERE r.target_deployment_id=d.id AND r.status NOT IN ('complete','failed'))
        AND NOT EXISTS (SELECT 1 FROM project_environment_promotion_workload_specs w JOIN project_environment_promotions p ON p.id=w.promotion_id WHERE w.deployment_id=d.id AND p.status='running')
        ORDER BY d.id LIMIT 32`, node)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var changed []Deployment
	for _, id := range ids {
		d, err := s.supersedeObsoleteMCPTaskHold(ctx, id, node)
		if err != nil {
			return changed, err
		}
		if d != nil {
			changed = append(changed, *d)
		}
	}
	return changed, nil
}

func (s *PgStore) supersedeObsoleteMCPTaskHold(ctx context.Context, id, node string) (*Deployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var appID string
	if err = tx.QueryRow(ctx, `SELECT app_id::text FROM deployments WHERE id=$1`, id).Scan(&appID); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	// Match activation/create lock order; the successor cannot change traffic
	// between this check and the terminal transition.
	if err = sqlc.New().LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return nil, err
	}
	var appStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM apps WHERE id=$1 FOR UPDATE`, appID).Scan(&appStatus); err != nil {
		return nil, err
	}
	d, err := scanDeploymentWithRootfs(tx.QueryRow(ctx, `SELECT `+deploymentSelectColumnsWithRootfs+` FROM deployments WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if appStatus == string(AppDeleted) || !d.Status.IsCancelEligible() || d.DeletedAt != nil || d.EnvironmentWorkloadRuntime != "" {
		return nil, nil
	}
	var successor string
	err = tx.QueryRow(ctx, `SELECT n.id::text FROM deployments n
        WHERE n.app_id=$2 AND n.scope=$3 AND n.revision>$4 AND n.status='live' AND n.traffic_percent>0 AND n.deleted_at IS NULL
        AND EXISTS (SELECT 1 FROM deployment_mcp_task_holds h WHERE h.deployment_id=$1 AND (h.owner_node='' OR h.owner_node=$5))
        AND NOT EXISTS (SELECT 1 FROM deployment_rollback_operations r WHERE r.target_deployment_id=$1 AND r.status NOT IN ('complete','failed'))
        AND NOT EXISTS (SELECT 1 FROM project_environment_promotion_workload_specs w JOIN project_environment_promotions p ON p.id=w.promotion_id WHERE w.deployment_id=$1 AND p.status='running')
        ORDER BY n.revision DESC LIMIT 1 FOR UPDATE OF n`, id, appID, normalizedDeploymentScope(d.Scope), d.Revision, node).Scan(&successor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE deployments SET status='superseded',traffic_percent=0 WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE deployment_mcp_task_holds SET recovery_payload=NULL,lease_until=NULL,wake_requested=false,
        claim_generation=claim_generation+1,last_recovery_outcome='inactive',last_recovery_at=clock_timestamp(),
        last_recovery_error_code='mcp_task_hold_superseded' WHERE deployment_id=$1`, id); err != nil {
		return nil, err
	}
	if err = appendMCPTaskAuditTx(ctx, tx, id, DeployMCPTaskRetryCompleted, "imaged:mcp_task_recovery", map[string]any{"outcome": "inactive", "reason": "newer_serving_revision", "superseded_by": successor}); err != nil {
		return nil, err
	}
	// Publish with the transition so an imaged crash after commit cannot
	// prevent the scheduler from receiving the supersession signal.
	if err = notifyRetiredCanarySiblingsTx(ctx, tx, appID, []struct {
		ID    string
		Prior int
	}{{ID: id}}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	d.Status = DeploySuperseded
	d.TrafficPercent = 0
	return &d, nil
}

func (m *MemStore) ReconcileObsoleteMCPTaskHolds(ctx context.Context, node string) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var changed []Deployment
	for id, hold := range m.mcpTaskPromotionHolds {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		d, ok := m.deployments[id]
		app, appExists := m.apps[d.AppID]
		if !ok || !appExists || !d.Status.IsCancelEligible() || d.DeletedAt != nil || d.EnvironmentWorkloadRuntime != "" || app.Status == AppDeleted || hold.recovery.NodeID != "" && hold.recovery.NodeID != node {
			continue
		}
		protected := false
		for _, r := range m.checkedRollbacks {
			if r.TargetDeploymentID == id && r.Status != "complete" && r.Status != "failed" {
				protected = true
				break
			}
		}
		for _, spec := range m.projectEnvironmentPromotionWorkloadSpecs {
			if spec.DeploymentID == id && m.projectEnvironmentPromotions[spec.PromotionID].Status == "running" {
				protected = true
				break
			}
		}
		if protected {
			continue
		}
		var successor Deployment
		for _, n := range m.deployments {
			if n.AppID == d.AppID && normalizedDeploymentScope(n.Scope) == normalizedDeploymentScope(d.Scope) && n.Revision > d.Revision && n.Status == DeployLive && n.TrafficPercent > 0 && n.DeletedAt == nil && n.Revision > successor.Revision {
				successor = n
			}
		}
		if successor.ID == "" {
			continue
		}
		if err := m.appendMCPTaskAuditLocked(id, DeployMCPTaskRetryCompleted, "imaged:mcp_task_recovery", map[string]any{"outcome": "inactive", "reason": "newer_serving_revision", "superseded_by": successor.ID}); err != nil {
			return changed, err
		}
		d.Status = DeploySuperseded
		d.TrafficPercent = 0
		m.putDeploymentLocked(id, d)
		hold.recovery.Payload = nil
		hold.recovery.leaseUntil = time.Time{}
		hold.recovery.wakeRequested = false
		hold.recovery.Generation++
		now := time.Now().UTC()
		hold.LastRecoveryOutcome = "inactive"
		hold.LastRecoveryAt = &now
		hold.LastRecoveryErrorCode = "mcp_task_hold_superseded"
		m.mcpTaskPromotionHolds[id] = hold
		changed = append(changed, d)
		if len(changed) == 32 {
			break
		}
	}
	return changed, nil
}
