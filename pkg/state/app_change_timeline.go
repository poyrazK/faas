package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AppChangeRow is one raw row from a change source the ADR-741 timeline reads
// through AppChangeSourceStore. Incidents, health history, and audit events
// come from their existing store seams instead.
type AppChangeRow struct {
	ID           int64
	At           time.Time
	Kind         string
	DeploymentID string
	RuleID       string
}

// AppChangeSourceStore exposes the per-app, time-windowed reads the change
// timeline needs from stores that previously had no such query. Every method
// scopes by account and app; rows are newest first and capped at limit. The
// runtime-config source uses Store.AppRuntimeConfigChangedAt: that table keeps
// one upserted row per app, so only the latest change exists.
type AppChangeSourceStore interface {
	ListAppDeploymentAuditBetween(ctx context.Context, accountID, appID string, since, until time.Time, limit int) ([]AppChangeRow, error)
	ListAppEdgeRuleChangesBetween(ctx context.Context, accountID, appID string, since, until time.Time, limit int) ([]AppChangeRow, error)
}

var (
	_ AppChangeSourceStore = (*PgStore)(nil)
	_ AppChangeSourceStore = (*MemStore)(nil)
)

func (s *PgStore) ListAppDeploymentAuditBetween(ctx context.Context, accountID, appID string, since, until time.Time, limit int) ([]AppChangeRow, error) {
	rows, err := sqlc.New().ListAppDeploymentAuditBetween(ctx, s.pool, sqlc.ListAppDeploymentAuditBetweenParams{
		AppID: appID, AccountID: accountID, Since: NewPgtypeTime(since), Until: NewPgtypeTime(until), RowLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list app deployment audit: %w", err)
	}
	out := make([]AppChangeRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, AppChangeRow{ID: row.ID, At: row.At.Time, Kind: row.Kind, DeploymentID: row.DeploymentID})
	}
	return out, nil
}

func (s *PgStore) ListAppEdgeRuleChangesBetween(ctx context.Context, accountID, appID string, since, until time.Time, limit int) ([]AppChangeRow, error) {
	rows, err := sqlc.New().ListAppEdgeRuleChangesBetween(ctx, s.pool, sqlc.ListAppEdgeRuleChangesBetweenParams{
		AppID: appID, AccountID: accountID, Since: NewPgtypeTime(since), Until: NewPgtypeTime(until), RowLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list app edge-rule changes: %w", err)
	}
	out := make([]AppChangeRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, AppChangeRow{ID: row.ID, At: row.CreatedAt.Time, Kind: row.Operation, RuleID: row.RuleID})
	}
	return out, nil
}

func (m *MemStore) ListAppDeploymentAuditBetween(_ context.Context, accountID, appID string, since, until time.Time, limit int) ([]AppChangeRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[appID]; !ok || app.AccountID != accountID {
		return nil, nil
	}
	owned := map[uuid.UUID]string{}
	for id, d := range m.deployments {
		if parsed, err := uuid.Parse(id); err == nil && d.AppID == appID {
			owned[parsed] = parsed.String()
		}
	}
	var out []AppChangeRow
	for _, row := range m.deploymentAudit {
		dep, ok := owned[row.DeploymentID]
		if !ok || row.At.Before(since) || !row.At.Before(until) {
			continue
		}
		out = append(out, AppChangeRow{ID: row.ID, At: row.At, Kind: string(row.Kind), DeploymentID: dep})
	}
	return newestAppChanges(out, limit), nil
}

// ListAppEdgeRuleChangesBetween is empty in memory: the edge-rule change log
// is a Postgres-only ledger (see EdgeRuleChangeLogStore).
func (m *MemStore) ListAppEdgeRuleChangesBetween(context.Context, string, string, time.Time, time.Time, int) ([]AppChangeRow, error) {
	return nil, nil
}

func newestAppChanges(rows []AppChangeRow, limit int) []AppChangeRow {
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].At.Equal(rows[j].At) {
			return rows[i].At.After(rows[j].At)
		}
		return rows[i].ID > rows[j].ID
	})
	if limit >= 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}
