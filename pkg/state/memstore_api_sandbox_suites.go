package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) GetAPISandboxSuite(_ context.Context, appID string) (api.APISandboxSuite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	suite, ok := m.apiSandboxSuites[appID]
	if !ok {
		return api.APISandboxSuite{}, ErrNotFound
	}
	suite.ScenarioIDs = slices.Clone(suite.ScenarioIDs)
	return suite, nil
}

func (m *MemStore) PutAPISandboxSuite(ctx context.Context, appID, expected string, ids []string, automatic *bool) (api.APISandboxSuite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.APISandboxSuite{}, err
	}
	if app, ok := m.apps[appID]; !ok || app.Status == AppDeleted {
		return api.APISandboxSuite{}, ErrNotFound
	}
	current := m.apiSandboxSuites[appID]
	updated, err := updateSandboxSuite(current, expected, ids, automatic)
	if err != nil {
		return api.APISandboxSuite{}, err
	}
	if updated.Revision != current.Revision {
		for _, id := range updated.ScenarioIDs {
			if _, ok := m.apiSandboxScenarioLibrary[appID][id]; !ok {
				return api.APISandboxSuite{}, ErrConflict
			}
		}
		if m.apiSandboxSuites == nil {
			m.apiSandboxSuites = make(map[string]api.APISandboxSuite)
		}
		m.apiSandboxSuites[appID] = updated
	}
	updated.ScenarioIDs = slices.Clone(updated.ScenarioIDs)
	return updated, nil
}

func (m *MemStore) CreateAPISandboxSuiteRun(ctx context.Context, run APISandboxSuiteRun) (APISandboxSuiteRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return APISandboxSuiteRun{}, err
	}
	if app, ok := m.apps[run.AppID]; !ok || app.Status == AppDeleted {
		return APISandboxSuiteRun{}, ErrNotFound
	}
	if _, ok := m.apiSandboxSuiteRuns[run.ID]; ok {
		return APISandboxSuiteRun{}, ErrConflict
	}
	for _, other := range m.apiSandboxSuiteRuns {
		if other.AppID == run.AppID && other.FinishedAt == nil {
			return APISandboxSuiteRun{}, ErrAPISandboxSuiteAlreadyActive
		}
	}
	suite := m.apiSandboxSuites[run.AppID]
	if suite.Revision != run.SuiteRevision || len(suite.ScenarioIDs) == 0 || (run.Automatic && !suite.RunBeforeApproval && !m.releaseApprovalPolicies[run.AppID].RequiresSandboxSuite()) {
		return APISandboxSuiteRun{}, ErrConflict
	}
	items := make([]APISandboxSuiteScenario, 0, len(suite.ScenarioIDs))
	for _, id := range suite.ScenarioIDs {
		entry, ok := m.apiSandboxScenarioLibrary[run.AppID][id]
		if !ok {
			return APISandboxSuiteRun{}, ErrConflict
		}
		items = append(items, APISandboxSuiteScenario{
			APISandboxSuiteScenarioResult: api.APISandboxSuiteScenarioResult{
				ScenarioID: id, ScenarioRevision: entry.revision, ScenarioName: entry.name,
			},
			ComparisonID: uuid.NewString(), definition: slices.Clone(entry.definition),
		})
	}
	if m.apiSandboxSuiteRuns == nil {
		m.apiSandboxSuiteRuns = make(map[string]APISandboxSuiteRun)
		m.apiSandboxSuiteScenarios = make(map[string][]APISandboxSuiteScenario)
	}
	run.ScenarioCount, run.NextAttemptAt = len(items), &run.CreatedAt
	m.apiSandboxSuiteRuns[run.ID], m.apiSandboxSuiteScenarios[run.ID] = run, items
	return run, nil
}

func (m *MemStore) GetAPISandboxSuiteRun(_ context.Context, id string) (APISandboxSuiteRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.apiSandboxSuiteRuns[id]
	if !ok {
		return APISandboxSuiteRun{}, ErrNotFound
	}
	return run, nil
}

func (m *MemStore) ListAPISandboxSuiteRuns(_ context.Context, candidateID string) ([]api.APISandboxSuiteRunSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs := make([]api.APISandboxSuiteRunSummary, 0)
	for _, run := range m.apiSandboxSuiteRuns {
		if run.CandidateDeploymentID == candidateID {
			runs = append(runs, run.APISandboxSuiteRunSummary)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		return sandboxComparisonBefore(b.CreatedAt, b.ID, a.CreatedAt, a.ID)
	})
	return runs[:min(len(runs), 12)], nil
}

func (m *MemStore) ListAPISandboxSuiteScenarios(_ context.Context, id string) ([]APISandboxSuiteScenario, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := slices.Clone(m.apiSandboxSuiteScenarios[id])
	for i := range items {
		items[i].definition = nil
	}
	return items, nil
}

func (m *MemStore) GetAPISandboxSuiteScenario(_ context.Context, id string, pos int) (APISandboxSuiteScenario, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.apiSandboxSuiteScenarios[id]
	if pos < 0 || pos >= len(items) {
		return APISandboxSuiteScenario{}, ErrNotFound
	}
	item := items[pos]
	item.definition = slices.Clone(item.definition)
	return item, nil
}

func (m *MemStore) ClaimAPISandboxSuiteRun(_ context.Context, now time.Time, lease time.Duration) (APISandboxSuiteRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var first APISandboxSuiteRun
	for _, run := range m.apiSandboxSuiteRuns {
		if run.NextAttemptAt == nil || run.NextAttemptAt.After(now) || run.LeaseUntil.After(now) {
			continue
		}
		if first.NextAttemptAt == nil || sandboxComparisonBefore(*run.NextAttemptAt, run.ID, *first.NextAttemptAt, first.ID) {
			first = run
		}
	}
	if first.ID == "" {
		return APISandboxSuiteRun{}, ErrNotFound
	}
	first.LeaseToken, first.LeaseUntil = uuid.NewString(), now.Add(lease)
	m.apiSandboxSuiteRuns[first.ID] = first
	return first, nil
}

func (m *MemStore) CheckpointAPISandboxSuiteRun(_ context.Context, run APISandboxSuiteRun, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxSuiteRuns[run.ID]
	if run.LeaseToken == "" || current.LeaseToken != run.LeaseToken || !current.LeaseUntil.After(now) || current.FinishedAt != nil {
		return ErrConflict
	}
	current.CompletedCount, current.NextAttemptAt = run.CompletedCount, run.NextAttemptAt
	current.FinishedAt, current.FailureCode = run.FinishedAt, run.FailureCode
	current.WaitingForCapacity = run.WaitingForCapacity
	current.LeaseToken, current.LeaseUntil = "", time.Time{}
	m.apiSandboxSuiteRuns[run.ID] = current
	return nil
}
