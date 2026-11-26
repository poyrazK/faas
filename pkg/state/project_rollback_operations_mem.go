package state

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/routemonitor"
)

func cloneProjectRollback(r ProjectRollbackRecord) ProjectRollbackRecord {
	if r.AutoUndo != nil {
		autoUndo := *r.AutoUndo
		r.AutoUndo = &autoUndo
	}
	r.Steps = slices.Clone(r.Steps)
	r.ReleaseReviews = maps.Clone(r.ReleaseReviews)
	r.HTTPWorkloads = slices.Clone(r.HTTPWorkloads)
	r.HealthProbes = maps.Clone(r.HealthProbes)
	for app, probes := range r.HealthProbes {
		r.HealthProbes[app] = slices.Clone(probes)
	}
	r.LiveTrafficPolicies = maps.Clone(r.LiveTrafficPolicies)
	for app, policy := range r.LiveTrafficPolicies {
		policy.Routes = routemonitor.CloneRoutes(policy.Routes)
		r.LiveTrafficPolicies[app] = policy
	}
	for i := range r.Steps {
		r.Steps[i].CrossedReleases = slices.Clone(r.Steps[i].CrossedReleases)
		if r.Steps[i].LiveTraffic != nil {
			traffic := *r.Steps[i].LiveTraffic
			r.Steps[i].LiveTraffic = &traffic
		}
		if r.Steps[i].PreCutoverHealth != nil {
			health := *r.Steps[i].PreCutoverHealth
			r.Steps[i].PreCutoverHealth = &health
		}
		if r.Steps[i].Health != nil {
			health := *r.Steps[i].Health
			r.Steps[i].Health = &health
		}
	}
	return r
}

func (m *MemStore) CreateProjectRollback(ctx context.Context, account string, r ProjectRollbackRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	return m.createProjectRollbackLocked(account, r)
}

func (m *MemStore) createProjectRollbackLocked(account string, r ProjectRollbackRecord) error {
	if p, ok := m.projects[r.ProjectID]; !ok || p.AccountID != account {
		return ErrNotFound
	}
	if _, ok := m.projectRollbackOperations[r.ID]; ok || r.Status == "running" && m.projectRollbackRunningLocked(r.ProjectID) {
		return ErrConflict
	}
	if m.projectRollbackOperations == nil {
		m.projectRollbackOperations = make(map[string]ProjectRollbackRecord)
	}
	r.nextRun = time.Now()
	m.projectRollbackOperations[r.ID] = cloneProjectRollback(r)
	return nil
}

func (m *MemStore) projectRollbackRunningLocked(project string) bool {
	for _, r := range m.projectRollbackOperations {
		if r.ProjectID == project && r.Status == "running" {
			return true
		}
	}
	return false
}

func (m *MemStore) GetProjectRollback(_ context.Context, account, project, id string) (ProjectRollbackRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.projectRollbackOperations[id]
	if !ok || r.ProjectID != project || m.projects[project].AccountID != account {
		return ProjectRollbackRecord{}, ErrNotFound
	}
	return cloneProjectRollback(r), nil
}

func (m *MemStore) ClaimProjectRollback(_ context.Context, lease time.Duration) (ProjectRollbackRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var next ProjectRollbackRecord
	for _, r := range m.projectRollbackOperations {
		if r.Status == "running" && !r.nextRun.After(now) && (next.ID == "" || r.nextRun.Before(next.nextRun)) {
			next = r
		}
	}
	if next.ID == "" {
		return next, ErrNotFound
	}
	next.Version++
	next.nextRun = now.Add(lease)
	m.projectRollbackOperations[next.ID] = next
	return cloneProjectRollback(next), nil
}

func (m *MemStore) SaveProjectRollback(_ context.Context, r ProjectRollbackRecord, delay time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.projectRollbackOperations[r.ID]
	now := time.Now()
	if !ok || old.ProjectID != r.ProjectID || old.Version != r.Version || old.Status != "running" || !old.nextRun.After(now) {
		return ErrConflict
	}
	r.RequestID, r.PlanHash, r.startRequest = old.RequestID, old.PlanHash, old.startRequest
	r.ResumePlanHash = old.ResumePlanHash
	r.Version++
	r.nextRun = now.Add(delay)
	m.projectRollbackOperations[r.ID] = cloneProjectRollback(r)
	m.finishProjectRollbackUndoArtifactHoldsLocked(r.ID, r.Status, now)
	return nil
}

func (m *MemStore) ResumeProjectRollback(ctx context.Context, account string, r ProjectRollbackRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	old, ok := m.projectRollbackOperations[r.ID]
	if !ok || old.ProjectID != r.ProjectID || old.Version != r.Version || old.Status != "blocked" || old.UndoOperationID != "" || m.projects[old.ProjectID].AccountID != account ||
		m.projectRollbackRunningLocked(old.ProjectID) {
		return ErrConflict
	}
	if err := m.checkProjectRollbackResumeReviewLocked(ctx, r.ProjectID); err != nil {
		return err
	}
	r.RequestID, r.PlanHash, r.startRequest = old.RequestID, old.PlanHash, old.startRequest
	r.Version++
	r.nextRun = time.Now()
	m.projectRollbackOperations[r.ID] = cloneProjectRollback(r)
	m.continueProjectRollbackUndoArtifactHoldsLocked(r.ID, r.ID, r.nextRun)
	return nil
}
