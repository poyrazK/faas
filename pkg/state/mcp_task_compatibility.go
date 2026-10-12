package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) PutMCPTaskCompatibilityReport(ctx context.Context, appID string, report MCPTaskCompatibilityReport) error {
	if report.Handlers == nil {
		return errors.New("state: MCP task compatibility handlers must be an array")
	}
	handlers, err := json.Marshal(report.Handlers)
	if err != nil {
		return fmt.Errorf("state: encode MCP task compatibility report for app %s: %w", appID, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var unchanged bool
	err = tx.QueryRow(ctx, `SELECT handlers = $2::jsonb AND truncated = $3 AND
        observed_at > clock_timestamp()-interval '45 seconds' AND observed_at <= clock_timestamp()+interval '5 seconds'
        FROM app_mcp_task_compatibility WHERE app_id=$1::uuid FOR UPDATE`, appID, handlers, report.Truncated).Scan(&unchanged)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app_mcp_task_compatibility (app_id, handlers, truncated, observed_at)
		VALUES ($1::uuid, $2::jsonb, $3::boolean, $4::timestamptz)
		ON CONFLICT (app_id) DO UPDATE
		   SET handlers = EXCLUDED.handlers,
		       truncated = EXCLUDED.truncated,
		       observed_at = EXCLUDED.observed_at
	`, appID, handlers, report.Truncated, report.ObservedAt)
	if err != nil {
		return fmt.Errorf("state: save MCP task compatibility report for app %s: %w", appID, err)
	}
	if !unchanged && !report.Truncated && time.Since(report.ObservedAt) <= 45*time.Second && time.Since(report.ObservedAt) >= -5*time.Second {
		if _, err := tx.Exec(ctx, `UPDATE deployment_mcp_task_holds h SET
            next_attempt_at=LEAST(h.next_attempt_at,clock_timestamp()+interval '5 seconds'),
            wake_requested=(h.wake_requested OR COALESCE(h.lease_until > clock_timestamp(),false))
            FROM deployments d WHERE h.deployment_id=d.id AND d.app_id=$1::uuid
            AND h.recovery_payload IS NOT NULL AND d.status IN ('pending','building','imaging','snapshotting')`, appID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) GetMCPTaskCompatibilityReport(ctx context.Context, appID string) (MCPTaskCompatibilityReport, error) {
	var report MCPTaskCompatibilityReport
	var handlers []byte
	err := s.pool.QueryRow(ctx, `
		SELECT handlers, truncated, observed_at
		  FROM app_mcp_task_compatibility
		 WHERE app_id = $1::uuid
	`, appID).Scan(&handlers, &report.Truncated, &report.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MCPTaskCompatibilityReport{}, ErrNotFound
	}
	if err != nil {
		return MCPTaskCompatibilityReport{}, fmt.Errorf("state: load MCP task compatibility report for app %s: %w", appID, err)
	}
	if err := json.Unmarshal(handlers, &report.Handlers); err != nil || report.Handlers == nil {
		if err == nil {
			err = errors.New("handlers must be an array")
		}
		return MCPTaskCompatibilityReport{}, fmt.Errorf("state: decode MCP task compatibility report for app %s: %w", appID, err)
	}
	return report, nil
}

func (m *MemStore) PutMCPTaskCompatibilityReport(_ context.Context, appID string, report MCPTaskCompatibilityReport) error {
	if report.Handlers == nil {
		return errors.New("state: MCP task compatibility handlers must be an array")
	}
	report = cloneMCPTaskCompatibilityReport(report)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcpTaskCompatibilityReports == nil {
		m.mcpTaskCompatibilityReports = make(map[string]MCPTaskCompatibilityReport)
	}
	previous, exists := m.mcpTaskCompatibilityReports[appID]
	changed := !exists || !reflect.DeepEqual(previous.Handlers, report.Handlers) || previous.Truncated != report.Truncated || time.Since(previous.ObservedAt) > 45*time.Second || time.Since(previous.ObservedAt) < -5*time.Second
	m.mcpTaskCompatibilityReports[appID] = report
	now := time.Now().UTC()
	age := now.Sub(report.ObservedAt)
	if changed && !report.Truncated && age <= 45*time.Second && age >= -5*time.Second {
		for id, hold := range m.mcpTaskPromotionHolds {
			d, ok := m.deployments[id]
			if !ok || d.AppID != appID || d.Status.IsTerminal() || d.Status == DeployLive || len(hold.recovery.Payload) == 0 {
				continue
			}
			due := now.Add(5 * time.Second)
			if hold.recovery.nextAttemptAt.After(due) {
				hold.recovery.nextAttemptAt = due
			}
			hold.recovery.wakeRequested = hold.recovery.wakeRequested || hold.recovery.leaseUntil.After(now)
			m.mcpTaskPromotionHolds[id] = hold
		}
	}
	return nil
}

func (m *MemStore) GetMCPTaskCompatibilityReport(_ context.Context, appID string) (MCPTaskCompatibilityReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	report, ok := m.mcpTaskCompatibilityReports[appID]
	if !ok {
		return MCPTaskCompatibilityReport{}, ErrNotFound
	}
	return cloneMCPTaskCompatibilityReport(report), nil
}

func cloneMCPTaskCompatibilityReport(report MCPTaskCompatibilityReport) MCPTaskCompatibilityReport {
	cloned := report
	cloned.Handlers = make([]MCPTaskHandlerCompatibility, 0, len(report.Handlers))
	for _, handler := range report.Handlers {
		item := handler
		item.Deployments = append([]MCPTaskHandlerDeploymentWorkers(nil), handler.Deployments...)
		cloned.Handlers = append(cloned.Handlers, item)
	}
	return cloned
}
