package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// AppCPUPolicyObservationFreshness bounds how long a live node's CPU-policy
// acknowledgement may be trusted. Schedd refreshes it more often while VMs
// remain resident.
const AppCPUPolicyObservationFreshness = 90 * time.Second

type AppCPUPolicyApplyTarget struct {
	AppID         string
	NodeID        string
	Slug          string
	Revision      int64
	CPUMillicores int
}

type AppCPUPolicyNodeState struct {
	NodeName        string
	DesiredRevision int64
	AppliedRevision int64
	ObservedAt      time.Time
	LastError       string
}

type AppCPUPolicyConvergenceStore interface {
	LatestAppCPUPolicyRevision(context.Context, string) (int64, error)
	ListPendingAppCPUPolicyTargets(context.Context, string, time.Duration, int) ([]AppCPUPolicyApplyTarget, error)
	RecordAppCPUPolicyApply(context.Context, string, string, int64, error) error
	ListServingAppCPUPolicyNodeStates(context.Context, string) ([]AppCPUPolicyNodeState, error)
}

var _ AppCPUPolicyConvergenceStore = (*PgStore)(nil)

func (s *PgStore) LatestAppCPUPolicyRevision(ctx context.Context, appID string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: app CPU policy has nil pool")
	}
	var revision int64
	if err := s.pool.QueryRow(ctx, `SELECT app_cpu_policy_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("state: latest app CPU policy revision: %w", err)
	}
	return revision, nil
}

func (s *PgStore) ListPendingAppCPUPolicyTargets(ctx context.Context, appID string, staleAfter time.Duration, limit int) ([]AppCPUPolicyApplyTarget, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: app CPU policy has nil pool")
	}
	if staleAfter <= 0 {
		staleAfter = AppCPUPolicyObservationFreshness / 2
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id::text, i.node_id::text, a.slug, a.app_cpu_policy_revision,
		       CASE WHEN a.cpu_millicores > 0 THEN a.cpu_millicores ELSE $4 END
		FROM apps a
	JOIN instances i ON i.app_id = a.id
	LEFT JOIN app_cpu_policy_node_status p ON p.app_id = a.id AND p.node_id = i.node_id
	WHERE a.status <> 'deleted'
	  AND i.node_id IS NOT NULL
	  AND i.state IN ('waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining')
	  AND ($1 = '' OR a.id = $1::uuid)
	  AND (COALESCE(p.applied_revision, 0) < a.app_cpu_policy_revision
	       OR COALESCE(p.observed_at, 'epoch'::timestamptz) < now() - ($2 * interval '1 second'))
	GROUP BY a.id, i.node_id, a.slug, a.app_cpu_policy_revision, a.cpu_millicores, p.observed_at
	ORDER BY COALESCE(p.observed_at, 'epoch'::timestamptz), a.id, i.node_id
	LIMIT $3
	`, appID, staleAfter.Seconds(), limit, api.DefaultAppCPUMillicores)
	if err != nil {
		return nil, fmt.Errorf("state: list pending app CPU policy targets: %w", err)
	}
	defer rows.Close()
	targets := make([]AppCPUPolicyApplyTarget, 0)
	for rows.Next() {
		var target AppCPUPolicyApplyTarget
		if err := rows.Scan(&target.AppID, &target.NodeID, &target.Slug, &target.Revision, &target.CPUMillicores); err != nil {
			return nil, fmt.Errorf("state: scan pending app CPU policy target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate pending app CPU policy targets: %w", err)
	}
	return targets, nil
}

func (s *PgStore) RecordAppCPUPolicyApply(ctx context.Context, appID, nodeID string, revision int64, applyErr error) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: app CPU policy has nil pool")
	}
	appID, nodeID = strings.TrimSpace(appID), strings.TrimSpace(nodeID)
	if appID == "" || nodeID == "" || revision <= 0 {
		return fmt.Errorf("state: invalid app CPU policy acknowledgement")
	}
	lastError := ""
	succeeded := applyErr == nil
	if applyErr != nil {
		lastError = safetext.Truncate(applyErr.Error(), 2048)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_cpu_policy_node_status
		    (app_id, node_id, applied_revision, attempted_revision, observed_at, last_error)
		VALUES ($1, $2, CASE WHEN $4 THEN $3 ELSE 0 END, $3, now(), $5)
		ON CONFLICT (app_id, node_id) DO UPDATE SET
		    applied_revision = CASE WHEN $4
		        THEN GREATEST(app_cpu_policy_node_status.applied_revision, EXCLUDED.applied_revision)
		        ELSE app_cpu_policy_node_status.applied_revision
		    END,
		    attempted_revision = GREATEST(app_cpu_policy_node_status.attempted_revision, EXCLUDED.attempted_revision),
		    observed_at = now(),
		    last_error = CASE WHEN EXCLUDED.attempted_revision >= app_cpu_policy_node_status.attempted_revision
		        THEN EXCLUDED.last_error ELSE app_cpu_policy_node_status.last_error END
	`, appID, nodeID, revision, succeeded, lastError)
	if err != nil {
		return fmt.Errorf("state: record app CPU policy apply: %w", err)
	}
	return nil
}

func (s *PgStore) ListServingAppCPUPolicyNodeStates(ctx context.Context, appID string) ([]AppCPUPolicyNodeState, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: app CPU policy has nil pool")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.name, a.app_cpu_policy_revision, COALESCE(p.applied_revision, 0),
		       COALESCE(p.observed_at, 'epoch'::timestamptz), COALESCE(p.last_error, '')
		FROM apps a
		JOIN instances i ON i.app_id = a.id
		JOIN compute_nodes n ON n.id = i.node_id
		LEFT JOIN app_cpu_policy_node_status p ON p.app_id = a.id AND p.node_id = i.node_id
		WHERE a.id = $1 AND a.status <> 'deleted' AND i.node_id IS NOT NULL
		  AND i.state IN ('waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining')
		GROUP BY n.name, a.app_cpu_policy_revision, p.applied_revision, p.observed_at, p.last_error
		ORDER BY n.name
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list serving app CPU policy node states: %w", err)
	}
	defer rows.Close()
	var nodes []AppCPUPolicyNodeState
	for rows.Next() {
		var node AppCPUPolicyNodeState
		if err := rows.Scan(&node.NodeName, &node.DesiredRevision, &node.AppliedRevision, &node.ObservedAt, &node.LastError); err != nil {
			return nil, fmt.Errorf("state: scan serving app CPU policy node state: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate serving app CPU policy node states: %w", err)
	}
	return nodes, nil
}
