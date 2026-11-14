package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DefaultCustomDomainBatchReader reads many apps' default custom domains in
// one query. The app list called DefaultCustomDomain once per app, one round
// trip after another; this is the same predicate for a set of apps.
type DefaultCustomDomainBatchReader interface {
	// DefaultCustomDomains maps each app with a usable default domain to it;
	// an app without one is absent.
	DefaultCustomDomains(ctx context.Context, appIDs []string) (map[string]string, error)
}

// DeploymentListDecorationReader reads the per-deployment decorations of a
// deployment list (worker drain summaries and MCP task promotion holds) in
// one query each instead of one per row. A deployment without a decoration
// is absent from the map.
type DeploymentListDecorationReader interface {
	DeploymentWorkerDrainStatuses(ctx context.Context, ids []string) (map[string]*DeploymentWorkerDrainStatus, error)
	MCPTaskPromotionHolds(ctx context.Context, ids []string) (map[string]*MCPTaskPromotionHold, error)
}

// DefaultCustomDomains is DefaultCustomDomain for a set of apps.
func (s *PgStore) DefaultCustomDomains(ctx context.Context, appIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(appIDs))
	if len(appIDs) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().ReadUsableCustomDomainDefaults(ctx, s.pool, sqlc.ReadUsableCustomDomainDefaultsParams{AppIds: appIDs, TenantSurfaces: customDomainTenantRouting(ctx)})
	if err != nil {
		return nil, fmt.Errorf("read usable custom-domain defaults: %w", err)
	}
	for _, row := range rows {
		out[row.AppID] = row.Domain
	}
	return out, nil
}

// DefaultCustomDomains is DefaultCustomDomain for a set of apps.
func (m *MemStore) DefaultCustomDomains(ctx context.Context, appIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(appIDs))
	for _, appID := range appIDs {
		domain, err := m.DefaultCustomDomain(ctx, appID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[appID] = domain
	}
	return out, nil
}

// DeploymentWorkerDrainStatuses is DeploymentWorkerDrainStatus for a set of
// deployments.
func (s *PgStore) DeploymentWorkerDrainStatuses(ctx context.Context, ids []string) (map[string]*DeploymentWorkerDrainStatus, error) {
	out := make(map[string]*DeploymentWorkerDrainStatus, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT deployment_id::text, data FROM (
        SELECT DISTINCT ON (deployment_id, data->>'instance_id') deployment_id, data, id FROM deployment_audit
        WHERE deployment_id = ANY($1::uuid[]) AND kind IN ('deploy.worker_drain_started','deploy.worker_drain_completed')
        ORDER BY deployment_id, data->>'instance_id', id DESC
    ) latest ORDER BY id DESC`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := map[string][]json.RawMessage{}
	for rows.Next() {
		var id string
		var raw json.RawMessage
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		records[id] = append(records[id], raw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for id, rs := range records {
		if status := summarizeWorkerDrains(rs, now); status != nil {
			out[id] = status
		}
	}
	return out, nil
}

// DeploymentWorkerDrainStatuses is DeploymentWorkerDrainStatus for a set of
// deployments.
func (m *MemStore) DeploymentWorkerDrainStatuses(ctx context.Context, ids []string) (map[string]*DeploymentWorkerDrainStatus, error) {
	out := make(map[string]*DeploymentWorkerDrainStatus, len(ids))
	for _, id := range ids {
		status, err := m.DeploymentWorkerDrainStatus(ctx, id)
		if err != nil {
			return nil, err
		}
		if status != nil {
			out[id] = status
		}
	}
	return out, nil
}

// MCPTaskPromotionHolds is MCPTaskPromotionHold for a set of deployments.
func (s *PgStore) MCPTaskPromotionHolds(ctx context.Context, ids []string) (map[string]*MCPTaskPromotionHold, error) {
	out := make(map[string]*MCPTaskPromotionHold, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text,h.detail,h.observed_at,h.first_held_at,h.attempts,
        CASE WHEN h.recovery_payload IS NOT NULL THEN GREATEST(h.next_attempt_at,COALESCE(h.lease_until,h.next_attempt_at)) END,
        COALESCE(h.lease_until > clock_timestamp(),false),h.last_recovery_outcome,h.last_recovery_at,h.last_recovery_error_code
        FROM deployment_mcp_task_holds h JOIN deployments d ON d.id = h.deployment_id
        WHERE d.id = ANY($1::uuid[]) AND d.status IN ('pending', 'building', 'imaging', 'snapshotting')`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var hold MCPTaskPromotionHold
		if err := rows.Scan(&id, &hold.Detail, &hold.ObservedAt, &hold.HeldAt, &hold.RecoveryAttempts, &hold.NextRetryAt, &hold.RecoveryInProgress, &hold.LastRecoveryOutcome, &hold.LastRecoveryAt, &hold.LastRecoveryErrorCode); err != nil {
			return nil, err
		}
		out[id] = &hold
	}
	return out, rows.Err()
}

// MCPTaskPromotionHolds is MCPTaskPromotionHold for a set of deployments; an
// unknown deployment has no hold.
func (m *MemStore) MCPTaskPromotionHolds(ctx context.Context, ids []string) (map[string]*MCPTaskPromotionHold, error) {
	out := make(map[string]*MCPTaskPromotionHold, len(ids))
	for _, id := range ids {
		hold, err := m.MCPTaskPromotionHold(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if hold != nil {
			out[id] = hold
		}
	}
	return out, nil
}
