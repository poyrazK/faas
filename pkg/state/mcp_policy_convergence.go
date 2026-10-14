package state

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"net/url"
)

const (
	AlertMetricMCPPolicyUnconvergedGateways AlertMetric = "mcp_policy_unconverged_gateways"
	AlertMetricMCPPolicyUnverified          AlertMetric = "mcp_policy_unverified"
)

type MCPPolicyConvergenceStore interface {
	MCPPolicyConvergenceSnapshot(context.Context, string, string) (api.MCPPolicyConvergenceSnapshot, error)
}

func (s *PgStore) MCPPolicyConvergenceSnapshot(ctx context.Context, accountID, appID string) (api.MCPPolicyConvergenceSnapshot, error) {
	row, err := sqlc.New().ReadMCPPolicyConvergenceSnapshot(ctx, s.pool, sqlc.ReadMCPPolicyConvergenceSnapshotParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return api.MCPPolicyConvergenceSnapshot{}, fmt.Errorf("state: MCP policy convergence: %w", mapErr(err))
	}
	result := api.MCPPolicyConvergenceSnapshot{State: "active", CapturedAt: row.CapturedAt.Time, DesiredRevision: row.Revision, ServingGateways: row.Serving, PendingGateways: row.Pending, StaleGateways: row.Stale, UnconvergedGateways: row.Unconverged, DeploymentID: pgUUIDString(row.DeploymentID), AdmissionID: row.AdmissionID, PolicyStatusPath: "/v1/apps/" + url.PathEscape(row.Slug) + "/policy/status?edge_rules_scope=account"}
	if !row.DeploymentID.Valid {
		result.State = "paused"
		result.DeploymentID = ""
	} else if row.Revision == 0 || row.Serving == 0 {
		result.State = "unverified"
	} else if row.Unconverged > 0 {
		result.State = "pending"
	}
	if result.AdmissionID != "" {
		result.AdmissionHistoryPath = "/v1/mcp/admissions/" + result.AdmissionID
	}
	return result, nil
}
