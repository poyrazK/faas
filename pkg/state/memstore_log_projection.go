package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

var _ LogProjectionStore = (*MemStore)(nil)

// ListDeploymentAuditForLedger mirrors the PostgreSQL keyset read: rows whose
// deployment still belongs to an app, after the cursor, before until.
func (m *MemStore) ListDeploymentAuditForLedger(_ context.Context, afterAt time.Time, afterID int64, until time.Time, limit int) ([]LedgerDeploymentAudit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []LedgerDeploymentAudit
	for _, audit := range m.deploymentAudit {
		if !afterKeyset(audit.At, audit.ID, afterAt, afterID) || !audit.At.Before(until) {
			continue
		}
		// MemStore ids are 32 hex characters; the audit row holds a UUID.
		deployment, ok := m.deployments[audit.DeploymentID.String()]
		if !ok {
			deployment, ok = m.deployments[strings.ReplaceAll(audit.DeploymentID.String(), "-", "")]
		}
		if !ok {
			continue
		}
		app, ok := m.apps[deployment.AppID]
		if !ok {
			continue
		}
		data := json.RawMessage(`{}`)
		if len(audit.Data) > 0 {
			data = append(json.RawMessage(nil), audit.Data...)
		}
		rows = append(rows, LedgerDeploymentAudit{
			ID: audit.ID, At: audit.At.UTC(), AccountID: app.AccountID, AppID: app.ID,
			DeploymentID: deployment.ID, Kind: audit.Kind, Actor: audit.Actor, Data: data,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return afterKeyset(rows[j].At, rows[j].ID, rows[i].At, rows[i].ID) })
	if limit = logProjectionLimit(limit); len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// ListBuildOutcomesForLedger mirrors the PostgreSQL keyset read over finished
// builds.
func (m *MemStore) ListBuildOutcomesForLedger(_ context.Context, afterAt time.Time, afterID string, until time.Time, limit int) ([]LedgerBuildOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []LedgerBuildOutcome
	for _, build := range m.builds {
		if build.FinishedAt.IsZero() || !afterKeyset(build.FinishedAt, build.ID, afterAt, afterID) || !build.FinishedAt.Before(until) {
			continue
		}
		deployment, ok := m.deployments[build.DeploymentID]
		if !ok {
			continue
		}
		app, ok := m.apps[deployment.AppID]
		if !ok {
			continue
		}
		rows = append(rows, LedgerBuildOutcome{
			BuildID: build.ID, AccountID: app.AccountID, AppID: app.ID, DeploymentID: deployment.ID,
			Kind: string(build.Kind), Status: build.Status, FailureClass: build.FailureClass,
			StartedAt: build.StartedAt.UTC(), FinishedAt: build.FinishedAt.UTC(), DeploymentError: deployment.Error,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return afterKeyset(rows[j].FinishedAt, rows[j].BuildID, rows[i].FinishedAt, rows[i].BuildID)
	})
	if limit = logProjectionLimit(limit); len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
