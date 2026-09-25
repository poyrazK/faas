package state

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ServingGatewayControlPlaneState joins a registered serving gateway with
// its most recent durable repair position. An epoch ObservedAt means that
// gateway has not reported a position yet.
type ServingGatewayControlPlaneState struct {
	NodeName             string
	LastChangeID         int64
	ObservedAt           time.Time
	LastEdgeRuleChangeID int64
	EdgeRulesObservedAt  time.Time
}

// ControlPlanePolicyStatusStore is additive to Store so in-memory test stores
// need not implement PostgreSQL-only gateway observations.
type ControlPlanePolicyStatusStore interface {
	LatestAppControlPlaneChangeID(context.Context, string) (int64, error)
	LatestAppEdgeRuleChangeID(context.Context, string) (int64, error)
	ListServingGatewayControlPlaneStates(context.Context) ([]ServingGatewayControlPlaneState, error)
	UpsertGatewayControlPlaneWatermark(context.Context, string, string, int64) error
	UpsertGatewayEdgeRuleWatermark(context.Context, string, string, int64) error
}

var _ ControlPlanePolicyStatusStore = (*PgStore)(nil)

// LatestAppControlPlaneChangeID is the desired revision for the first policy
// status slice: gateway app-cache invalidation and deployment traffic weights.
// It deliberately excludes unrelated future ledger resource types until their
// consumers also publish applied positions.
func (s *PgStore) LatestAppControlPlaneChangeID(ctx context.Context, appID string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: control-plane policy status has nil pool")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(id), 0)
		FROM control_plane_change_log
		WHERE app_id = $1 AND resource_type IN ('app', 'deployment_traffic')
	`, appID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("state: latest app control-plane change: %w", err)
	}
	return id, nil
}

// ListServingGatewayControlPlaneStates uses the same active compute-node
// membership predicate as the edge-rule barrier. A missing watermark stays
// visible with revision zero, so the API cannot silently call it active.
func (s *PgStore) ListServingGatewayControlPlaneStates(ctx context.Context) ([]ServingGatewayControlPlaneState, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: control-plane policy status has nil pool")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.name,
		       COALESCE(w.last_change_id, 0),
	       COALESCE(w.observed_at, 'epoch'::timestamptz),
	       COALESCE(e.last_change_id, 0),
	       COALESCE(e.observed_at, 'epoch'::timestamptz)
		FROM compute_nodes n
		LEFT JOIN gateway_control_plane_watermarks w ON w.node_name = n.name
		LEFT JOIN gateway_edge_rule_watermarks e ON e.node_name = n.name
		WHERE n.active = true
		  AND n.role IN ('compute-only', 'compute-node')
		  AND n.gateway_target_url IS NOT NULL
		  AND btrim(n.gateway_target_url) <> ''
		ORDER BY n.name
	`)
	if err != nil {
		return nil, fmt.Errorf("state: list serving gateway control-plane states: %w", err)
	}
	defer rows.Close()
	var states []ServingGatewayControlPlaneState
	for rows.Next() {
		var state ServingGatewayControlPlaneState
		if err := rows.Scan(&state.NodeName, &state.LastChangeID, &state.ObservedAt,
			&state.LastEdgeRuleChangeID, &state.EdgeRulesObservedAt); err != nil {
			return nil, fmt.Errorf("state: scan serving gateway control-plane state: %w", err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate serving gateway control-plane states: %w", err)
	}
	return states, nil
}

// UpsertGatewayEdgeRuleWatermark is called after edge-rule cache repair. It
// has its own boot epoch and cursor because edge-rule IDs come from a separate
// ledger sequence than control-plane IDs.
func (s *PgStore) UpsertGatewayEdgeRuleWatermark(ctx context.Context, nodeName, bootID string, lastChangeID int64) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: edge-rule policy status has nil pool")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" || bootID == "" || lastChangeID < 0 {
		return fmt.Errorf("state: invalid gateway edge-rule watermark")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO gateway_edge_rule_watermarks
		    (node_name, boot_id, last_change_id, observed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (node_name) DO UPDATE SET
		    boot_id = EXCLUDED.boot_id,
		    last_change_id = CASE
		        WHEN gateway_edge_rule_watermarks.boot_id = EXCLUDED.boot_id
		        THEN GREATEST(gateway_edge_rule_watermarks.last_change_id, EXCLUDED.last_change_id)
		        ELSE EXCLUDED.last_change_id
		    END,
		    observed_at = now()
	`, nodeName, bootID, lastChangeID)
	if err != nil {
		return fmt.Errorf("state: upsert gateway edge-rule watermark: %w", err)
	}
	return nil
}

// UpsertGatewayControlPlaneWatermark is called only after local replay
// succeeds. A new boot resets a prior process's watermark; retries within a
// boot may only move it forward. Each successful tick refreshes observed_at.
func (s *PgStore) UpsertGatewayControlPlaneWatermark(ctx context.Context, nodeName, bootID string, lastChangeID int64) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: control-plane policy status has nil pool")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" || bootID == "" || lastChangeID < 0 {
		return fmt.Errorf("state: invalid gateway control-plane watermark")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO gateway_control_plane_watermarks
		    (node_name, boot_id, last_change_id, observed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (node_name) DO UPDATE SET
		    boot_id = EXCLUDED.boot_id,
		    last_change_id = CASE
		        WHEN gateway_control_plane_watermarks.boot_id = EXCLUDED.boot_id
		        THEN GREATEST(gateway_control_plane_watermarks.last_change_id, EXCLUDED.last_change_id)
		        ELSE EXCLUDED.last_change_id
		    END,
		    observed_at = now()
	`, nodeName, bootID, lastChangeID)
	if err != nil {
		return fmt.Errorf("state: upsert gateway control-plane watermark: %w", err)
	}
	return nil
}
