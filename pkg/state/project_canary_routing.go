package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func selectOwnedCanary(p ProjectCanaryPolicyState, active, key string) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	if active != canaryActiveID(p) {
		if p.Status == "needs_operator" {
			return "", nil
		}
		return "", ErrConflict
	}
	if p.Control != nil && p.Control.Status == "pending" || p.Status == "needs_operator" || !time.Now().Before(p.Deadline) {
		if p.Percent < 100 {
			return p.BaselineReleaseID, nil
		}
		return "", ErrConflict
	}
	return SelectProjectCanaryRelease(p, key, time.Now().UTC())
}
func selectProjectCanaryDB(ctx context.Context, db sqlc.DBTX, appID, key string) (string, error) {
	row, err := sqlc.New().ReadProjectCanaryRouting(ctx, db, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var p ProjectCanaryPolicyState
	if err = json.Unmarshal(row.Policy, &p); err != nil {
		return "", err
	}
	active, err := sqlc.New().ActiveSafeProjectReleaseID(ctx, db, pgUUIDString(row.ProjectID))
	if err != nil {
		return "", mapErr(err)
	}
	return selectOwnedCanary(p, active, key)
}
func (s *PgStore) ResolveProjectCanary(ctx context.Context, appID, scope, key string) (string, string, error) {
	requested := ""
	if normalizedDeploymentScope(scope) == "production" {
		var err error
		requested, err = selectProjectCanaryDB(ctx, s.pool, appID, key)
		if err != nil {
			return "", "", err
		}
	}
	return s.ResolveProjectRelease(ctx, appID, scope, requested)
}
func (m *MemStore) selectProjectCanaryLocked(appID, key string) (string, error) {
	app := m.apps[appID]
	var latest *projectCanaryRecord
	for _, record := range m.projectCanaries {
		if record.AccountID != app.AccountID || record.ProjectID != app.ProjectID || record.Policy.Status != "running" && record.Policy.Status != "needs_operator" {
			continue
		}
		if latest == nil || record.Policy.Status == "running" && latest.Policy.Status != "running" || record.Policy.Status == latest.Policy.Status && latest.Policy.StageStartedAt.Before(record.Policy.StageStartedAt) {
			copy := record
			latest = &copy
		}
	}
	if latest == nil {
		return "", nil
	}
	return selectOwnedCanary(latest.Policy, m.activeProjectReleaseSets[releaseKey(app.ProjectID, "production")], key)
}
func (m *MemStore) ResolveProjectCanary(_ context.Context, appID, scope, key string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	requested := ""
	if normalizedDeploymentScope(scope) == "production" {
		var err error
		requested, err = m.selectProjectCanaryLocked(appID, key)
		if err != nil {
			return "", "", err
		}
	}
	return m.resolveProjectReleaseLocked(appID, scope, requested)
}

// ADR-570: the invocation version snapshot selects a canary in its own
// committed view; both reads are lock-free.
func (s invocationVersionPolicyReader) ResolveProjectCanary(ctx context.Context, appID, scope, key string) (string, string, error) {
	requested := ""
	if normalizedDeploymentScope(scope) == "production" {
		var err error
		if requested, err = selectProjectCanaryDB(ctx, s.tx, appID, key); err != nil {
			return "", "", err
		}
	}
	return s.ResolveProjectRelease(ctx, appID, scope, requested)
}

// The public routing snapshot applies the same canary to unpinned traffic.
func (s publicRoutingPolicyReader) ResolvePublicProjectCanary(ctx context.Context, appID, scope, key string) (string, string, error) {
	requested := ""
	if normalizedDeploymentScope(scope) == "production" {
		var err error
		if requested, err = selectProjectCanaryDB(ctx, s.tx, appID, key); err != nil {
			return "", "", err
		}
	}
	return s.ResolvePublicProjectRelease(ctx, appID, scope, requested)
}

// The snapshot already holds the store lock.
func (s memInvocationVersionReader) ResolveProjectCanary(ctx context.Context, appID, scope, key string) (string, string, error) {
	requested := ""
	if normalizedDeploymentScope(scope) == "production" {
		var err error
		if requested, err = s.store.selectProjectCanaryLocked(appID, key); err != nil {
			return "", "", err
		}
	}
	return s.ResolveProjectRelease(ctx, appID, scope, requested)
}
func (m *MemStore) canaryGraphCapturedLocked(accountID, projectID, graphID string) bool {
	for _, c := range m.projectCanaries {
		if c.AccountID == accountID && c.ProjectID == projectID && (c.Policy.Status == "running" || c.Policy.Status == "needs_operator" || c.Policy.Status == "complete" && c.Policy.Stabilizing) && (c.Policy.CandidateReleaseID == graphID || c.Policy.BaselineReleaseID == graphID) && m.activeProjectReleaseSets[releaseKey(projectID, "production")] == canaryActiveID(c.Policy) && releasePubliclyUsable(m.projectReleaseSets[graphID], time.Now()) {
			return true
		}
	}
	return false
}
func (m *MemStore) canaryWorkerTargetsLocked(appID string) []string {
	app := m.apps[appID]
	if app.Status != AppActive {
		return nil
	}
	var ids []string
	for _, c := range m.projectCanaries {
		eligibleStatus := c.Policy.Status == "running" || c.Policy.Status == "complete" && c.Policy.Stabilizing
		if c.WorkerAppID != appID || !eligibleStatus || !time.Now().Before(c.Policy.Deadline) || m.activeProjectReleaseSets[releaseKey(app.ProjectID, "production")] != canaryActiveID(c.Policy) {
			continue
		}
		var r struct {
			Apply struct {
				SafeRelease *api.ProjectSafeRelease `json:"safe_release"`
			} `json:"apply"`
		}
		if json.Unmarshal(m.projectSubmissions[c.AccountID+"\x00"+c.AttemptID].Payload, &r) != nil || r.Apply.SafeRelease == nil || r.Apply.SafeRelease.Worker == nil {
			continue
		}
		if c.Policy.Status == "complete" && !api.ProjectReleaseMonitoring(r.Apply.SafeRelease) {
			continue
		}
		b := m.queueBindings[r.Apply.SafeRelease.Worker.BindingID]
		if b.AppID != appID || b.AccountID != app.AccountID || b.DeploymentScope != "production" || b.Mode != "push" || b.WorkloadClass != WorkloadClassWorker || !b.Enabled || b.RetiredAt != nil {
			continue
		}
		for _, id := range []string{c.Policy.BaselineReleaseID, c.Policy.CandidateReleaseID} {
			graph := m.projectReleaseSets[id]
			dep := m.deployments[releaseMemberForApp(graph, appID)]
			if graph.AccountID == app.AccountID && graph.ProjectID == app.ProjectID && graph.EnvironmentSlug == "production" && releasePubliclyUsable(graph, time.Now()) && dep.AppID == appID && dep.Scope == "production" && dep.Status == DeployLive {
				ids = append(ids, dep.ID)
			}
		}
	}
	return ids
}
