package state

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/safetext"
)

// AppEgressPolicyObservationFreshness bounds how long runtime status may trust
// a VM node's last successful egress-policy acknowledgement. The scheduler
// refreshes acknowledgements more frequently than this while an app is live.
const AppEgressPolicyObservationFreshness = 90 * time.Second

// AppEgressPolicyApplyTarget is the latest desired app egress policy for one
// node currently hosting a live instance of the app.
type AppEgressPolicyApplyTarget struct {
	AppID     string
	NodeID    string
	Slug      string
	Revision  int64
	Allowlist []netip.Prefix
	// EgressPorts (ADR-361) are the app's declared extra egress ports; the
	// same revision covers them, so one apply converges both.
	EgressPorts []int
}

// AppEgressPolicyNodeState is the last durable acknowledgement for one live
// app/node pair. A zero ObservedAt means no apply attempt has been recorded.
type AppEgressPolicyNodeState struct {
	NodeName        string
	DesiredRevision int64
	AppliedRevision int64
	ObservedAt      time.Time
	LastError       string
}

// AppEgressPolicyConvergenceStore is additive to Store so in-memory tests and
// small Store implementations do not need PostgreSQL-only apply watermarks.
type AppEgressPolicyConvergenceStore interface {
	LatestAppEgressPolicyRevision(context.Context, string) (int64, error)
	ListPendingAppEgressPolicyTargets(context.Context, string, time.Duration, int) ([]AppEgressPolicyApplyTarget, error)
	RecordAppEgressPolicyApply(context.Context, string, string, int64, error) error
	ListServingAppEgressPolicyNodeStates(context.Context, string) ([]AppEgressPolicyNodeState, error)
}

var _ AppEgressPolicyConvergenceStore = (*PgStore)(nil)

func (s *PgStore) LatestAppEgressPolicyRevision(ctx context.Context, appID string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: app egress policy has nil pool")
	}
	var revision int64
	if err := s.pool.QueryRow(ctx, `
		SELECT egress_allowlist_revision
		FROM apps
		WHERE id = $1
	`, appID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("state: latest app egress policy revision: %w", err)
	}
	return revision, nil
}

// ListPendingAppEgressPolicyTargets returns current desired state for live
// app/node pairs whose revision is unapplied or whose successful observation
// has aged out. An empty appID scans all apps; a concrete ID handles a fast
// notification path. The result is bounded so a scheduler repair tick remains
// predictable even when the database contains many live workloads.
func (s *PgStore) ListPendingAppEgressPolicyTargets(ctx context.Context, appID string, staleAfter time.Duration, limit int) ([]AppEgressPolicyApplyTarget, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: app egress policy has nil pool")
	}
	if staleAfter <= 0 {
		staleAfter = AppEgressPolicyObservationFreshness / 2
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id::text,
		       i.node_id::text,
		       a.slug,
		       a.egress_allowlist_revision,
		       a.egress_allowlist::text,
		       a.egress_ports
		FROM apps a
	JOIN instances i ON i.app_id = a.id
	LEFT JOIN app_egress_policy_node_status p
	       ON p.app_id = a.id AND p.node_id = i.node_id
	WHERE a.status <> 'deleted'
	  AND i.node_id IS NOT NULL
	  AND i.state IN ('waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining')
	  AND ($1 = '' OR a.id = $1::uuid)
	  AND (
	       COALESCE(p.applied_revision, 0) < a.egress_allowlist_revision
	       OR COALESCE(p.observed_at, 'epoch'::timestamptz) < now() - ($2 * interval '1 second')
	  )
	GROUP BY a.id, i.node_id, a.slug, a.egress_allowlist_revision, a.egress_allowlist, a.egress_ports, p.observed_at
	ORDER BY COALESCE(p.observed_at, 'epoch'::timestamptz), a.id, i.node_id
	LIMIT $3
	`, appID, staleAfter.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("state: list pending app egress policy targets: %w", err)
	}
	defer rows.Close()

	targets := make([]AppEgressPolicyApplyTarget, 0)
	for rows.Next() {
		var target AppEgressPolicyApplyTarget
		var allowlistText string
		var egressPorts []int32
		if err := rows.Scan(&target.AppID, &target.NodeID, &target.Slug, &target.Revision, &allowlistText, &egressPorts); err != nil {
			return nil, fmt.Errorf("state: scan pending app egress policy target: %w", err)
		}
		target.Allowlist = cidrTextToPrefixes(allowlistText)
		target.EgressPorts = egressPortsFromDB(egressPorts)
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate pending app egress policy targets: %w", err)
	}
	return targets, nil
}

// RecordAppEgressPolicyApply persists an attempt. applied_revision advances
// only after the VMM RPC succeeds; failures remain pending for the next repair
// pass. Older, late-arriving attempts cannot overwrite a newer error/result.
func (s *PgStore) RecordAppEgressPolicyApply(ctx context.Context, appID, nodeID string, revision int64, applyErr error) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: app egress policy has nil pool")
	}
	appID = strings.TrimSpace(appID)
	nodeID = strings.TrimSpace(nodeID)
	if appID == "" || nodeID == "" || revision <= 0 {
		return fmt.Errorf("state: invalid app egress policy apply acknowledgement")
	}
	lastError := ""
	succeeded := applyErr == nil
	if applyErr != nil {
		lastError = safetext.Truncate(applyErr.Error(), 2048)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_egress_policy_node_status
		    (app_id, node_id, applied_revision, attempted_revision, observed_at, last_error)
		VALUES ($1, $2, CASE WHEN $4 THEN $3 ELSE 0 END, $3, now(), $5)
		ON CONFLICT (app_id, node_id) DO UPDATE SET
		    applied_revision = CASE WHEN $4
		        THEN GREATEST(app_egress_policy_node_status.applied_revision, EXCLUDED.applied_revision)
		        ELSE app_egress_policy_node_status.applied_revision
		    END,
		    attempted_revision = GREATEST(app_egress_policy_node_status.attempted_revision, EXCLUDED.attempted_revision),
		    observed_at = now(),
		    last_error = CASE
		        WHEN EXCLUDED.attempted_revision >= app_egress_policy_node_status.attempted_revision
		        THEN EXCLUDED.last_error
		        ELSE app_egress_policy_node_status.last_error
		    END
	`, appID, nodeID, revision, succeeded, lastError)
	if err != nil {
		return fmt.Errorf("state: record app egress policy apply: %w", err)
	}
	return nil
}

func (s *PgStore) ListServingAppEgressPolicyNodeStates(ctx context.Context, appID string) ([]AppEgressPolicyNodeState, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: app egress policy has nil pool")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.name,
		       a.egress_allowlist_revision,
		       COALESCE(p.applied_revision, 0),
		       COALESCE(p.observed_at, 'epoch'::timestamptz),
		       COALESCE(p.last_error, '')
		FROM apps a
	JOIN instances i ON i.app_id = a.id
	JOIN compute_nodes n ON n.id = i.node_id
	LEFT JOIN app_egress_policy_node_status p
	       ON p.app_id = a.id AND p.node_id = i.node_id
	WHERE a.id = $1
	  AND a.status <> 'deleted'
	  AND i.node_id IS NOT NULL
	  AND i.state IN ('waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining')
	GROUP BY n.name, a.egress_allowlist_revision, p.applied_revision, p.observed_at, p.last_error
	ORDER BY n.name
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list serving app egress policy node states: %w", err)
	}
	defer rows.Close()
	var nodes []AppEgressPolicyNodeState
	for rows.Next() {
		var node AppEgressPolicyNodeState
		if err := rows.Scan(&node.NodeName, &node.DesiredRevision, &node.AppliedRevision, &node.ObservedAt, &node.LastError); err != nil {
			return nil, fmt.Errorf("state: scan serving app egress policy node state: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate serving app egress policy node states: %w", err)
	}
	return nodes, nil
}
