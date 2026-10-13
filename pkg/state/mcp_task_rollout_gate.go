package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const mcpTaskMigrationOverrideTable = "deployment_mcp_task_migration_overrides"

func CheckMCPTaskCompatibilityReport(report MCPTaskCompatibilityReport, candidateDeploymentID string) error {
	return checkMCPTaskCompatibilityReport(report, candidateDeploymentID)
}

func checkMCPTaskCompatibilityReport(report MCPTaskCompatibilityReport, candidateDeploymentID string) error {
	if report.Handlers == nil {
		return fmt.Errorf("%w: compatibility report has no handlers array", ErrMCPTaskCompatibilityGate)
	}
	if report.Truncated {
		return fmt.Errorf("%w: compatibility report is truncated", ErrMCPTaskCompatibilityGate)
	}
	for _, handler := range report.Handlers {
		if handler.WaitingTasks <= 0 {
			continue
		}
		compatible := false
		for _, deployment := range handler.Deployments {
			if sameDeploymentID(deployment.DeploymentID, candidateDeploymentID) && deployment.Workers > 0 {
				compatible = true
				break
			}
		}
		if !compatible {
			return fmt.Errorf("%w: %d outstanding task(s) require tool %q handler version %q, but candidate deployment %s has no compatible worker",
				ErrMCPTaskCompatibilityGate, handler.WaitingTasks, handler.ToolName, handler.HandlerVersion, candidateDeploymentID)
		}
	}
	return nil
}

func checkMCPTaskCompatibilityCutoverTx(ctx context.Context, tx pgx.Tx, appID, scope, candidateDeploymentID string) error {
	var hasLiveSibling bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM deployments
		 WHERE app_id=$1 AND scope=$2 AND status='live' AND id<>$3
	)`, appID, normalizedDeploymentScope(scope), candidateDeploymentID).Scan(&hasLiveSibling); err != nil {
		return fmt.Errorf("state: check MCP task rollout siblings: %w", err)
	}
	if !hasLiveSibling {
		return nil
	}
	return checkMCPTaskCompatibilityReportTx(ctx, tx, appID, candidateDeploymentID)
}

func checkMCPTaskCompatibilityReportTx(ctx context.Context, tx pgx.Tx, appID, candidateDeploymentID string) error {
	var override bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM `+mcpTaskMigrationOverrideTable+`
		 WHERE deployment_id=$1 AND used_at IS NULL
	)`, candidateDeploymentID).Scan(&override); err != nil {
		return fmt.Errorf("state: check MCP task migration override: %w", err)
	}
	if override {
		return nil
	}
	var handlersJSON []byte
	var report MCPTaskCompatibilityReport
	var fresh bool
	err := tx.QueryRow(ctx, `SELECT handlers, truncated, observed_at,
		observed_at > clock_timestamp()-interval '45 seconds' AND observed_at <= clock_timestamp()+interval '5 seconds'
	  FROM app_mcp_task_compatibility WHERE app_id=$1`, appID).Scan(
		&handlersJSON, &report.Truncated, &report.ObservedAt, &fresh)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: read MCP task compatibility gate report: %w", err)
	}
	if !fresh {
		return fmt.Errorf("%w: compatibility report is stale; refresh the MCP task observer or set mcp_task_migration_override on this deployment",
			ErrMCPTaskCompatibilityGate)
	}
	if err := json.Unmarshal(handlersJSON, &report.Handlers); err != nil {
		return fmt.Errorf("%w: compatibility report could not be decoded", ErrMCPTaskCompatibilityGate)
	}
	return checkMCPTaskCompatibilityReport(report, candidateDeploymentID)
}

func consumeMCPTaskMigrationOverrideTx(ctx context.Context, tx pgx.Tx, deploymentID string) error {
	_, err := tx.Exec(ctx, `UPDATE `+mcpTaskMigrationOverrideTable+`
		SET used_at=clock_timestamp() WHERE deployment_id=$1 AND used_at IS NULL`, deploymentID)
	if err != nil {
		return fmt.Errorf("state: consume MCP task migration override: %w", err)
	}
	return nil
}

func (m *MemStore) checkMCPTaskCompatibilityCutoverLocked(deployment Deployment) error {
	hasLiveSibling := false
	for id, other := range m.deployments {
		if id != deployment.ID && other.AppID == deployment.AppID &&
			normalizedDeploymentScope(other.Scope) == normalizedDeploymentScope(deployment.Scope) && other.Status == DeployLive {
			hasLiveSibling = true
			break
		}
	}
	if !hasLiveSibling {
		return nil
	}
	return m.checkMCPTaskCompatibilityReportLocked(deployment)
}

func (m *MemStore) checkMCPTaskCompatibilityReportLocked(deployment Deployment) error {
	if deployment.MCPTaskMigrationOverride {
		return nil
	}
	report, exists := m.mcpTaskCompatibilityReports[deployment.AppID]
	if !exists {
		return nil
	}
	age := time.Since(report.ObservedAt)
	if age < 0 || age > 45*time.Second {
		return fmt.Errorf("%w: compatibility report is stale; refresh the MCP task observer or set mcp_task_migration_override on this deployment",
			ErrMCPTaskCompatibilityGate)
	}
	return checkMCPTaskCompatibilityReport(report, deployment.ID)
}
