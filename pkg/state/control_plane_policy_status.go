package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ServingGatewayControlPlaneState joins a registered serving gateway with
// its current process's durable repair position. A zero ObservedAt means
// no acknowledgement from a process with a fresh serving report.
type ServingGatewayControlPlaneState struct {
	NodeName                      string
	DatabaseNow                   time.Time
	LastChangeID                  int64
	ObservedAt                    time.Time
	LastEdgeRuleChangeID          int64
	EdgeRulesObservedAt           time.Time
	LastCorsPresetChangeID        int64
	CorsPresetsObservedAt         time.Time
	LastResponseCachePurgeID      int64
	ResponseCachePurgesObservedAt time.Time
}

// ControlPlanePolicyStatusStore is additive to Store so in-memory test stores
// need not implement PostgreSQL-only gateway observations.
type ControlPlanePolicyStatusStore interface {
	LatestAppControlPlaneChangeID(context.Context, string) (int64, error)
	LatestAppRequestPolicyRevision(context.Context, string) (int64, error)
	LatestAppEdgeRuleChangeID(context.Context, string) (int64, error)
	LatestAccountCorsPresetChangeID(context.Context, string) (int64, error)
	ListServingGatewayControlPlaneStates(context.Context) ([]ServingGatewayControlPlaneState, error)
	UpsertGatewayControlPlaneWatermark(context.Context, string, string, int64) error
	UpsertGatewayEdgeRuleWatermark(context.Context, string, string, int64) error
	UpsertGatewayCorsPresetWatermark(context.Context, string, string, int64) error
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

// LatestAppRequestPolicyRevision is the latest durable app-row revision read
// by the gateway request path. It intentionally excludes deployment traffic
// changes: those can lag independently while the request envelope is already
// current. The gateway's app-cache watermark is the applied position because
// replay invalidates its cached app projection before advancing that cursor.
func (s *PgStore) LatestAppRequestPolicyRevision(ctx context.Context, appID string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: request policy status has nil pool")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(id), 0)
		FROM control_plane_change_log
		WHERE app_id = $1 AND resource_type = 'app'
	`, appID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("state: latest app request policy revision: %w", err)
	}
	return id, nil
}

// ListServingGatewayControlPlaneStates accepts only the current process's
// acknowledgements while its serving report is fresh. Missing progress remains
// visible at zero; legacy watermarks cannot establish convergence.
func (s *PgStore) ListServingGatewayControlPlaneStates(ctx context.Context) ([]ServingGatewayControlPlaneState, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: control-plane policy status has nil pool")
	}
	rows, err := sqlc.New().ListServingGatewayPolicyProgress(ctx, s.pool, sqlc.ListServingGatewayPolicyProgressParams{
		FreshnessSeconds: api.TrafficRuntimeObservationFreshness.Seconds(), RowLimit: api.TrafficRuntimeObservationMaxNodes + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("state: list serving gateway policy progress: %w", err)
	}
	if len(rows) > api.TrafficRuntimeObservationMaxNodes {
		return nil, fmt.Errorf("state: gateway policy roster exceeds limit")
	}
	out := make([]ServingGatewayControlPlaneState, len(rows))
	for i, r := range rows {
		out[i] = ServingGatewayControlPlaneState{
			NodeName: r.NodeName, DatabaseNow: r.DatabaseNow.Time, LastChangeID: r.LastChangeID, ObservedAt: r.ObservedAt.Time,
			LastEdgeRuleChangeID: r.EdgeRuleChangeID, EdgeRulesObservedAt: r.EdgeRulesObservedAt.Time,
			LastCorsPresetChangeID: r.CorsPresetChangeID, CorsPresetsObservedAt: r.CorsPresetsObservedAt.Time,
			LastResponseCachePurgeID: r.CachePurgeChangeID, ResponseCachePurgesObservedAt: r.CachePurgesObservedAt.Time,
		}
	}
	return out, nil
}

// UpsertGatewayEdgeRuleWatermark preserves the legacy writer API. These
// unfenced rows are not read by convergence status or ledger pruning.
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

// UpsertGatewayControlPlaneWatermark preserves the legacy writer API.
// Current gateways publish through ReportGatewayPolicyProgress instead.
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
