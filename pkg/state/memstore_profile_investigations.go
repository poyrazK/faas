package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) profileInvestigationOwnedLocked(accountID, appID string) bool {
	a, ok := m.apps[appID]
	return ok && a.AccountID == accountID && a.Status != AppDeleted
}

func (m *MemStore) GetProfileInvestigation(_ context.Context, accountID, appID, id string) (api.ProfileInvestigation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileInvestigations[id]
	if !ok || row.AppID != appID || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileInvestigation{}, ErrNotFound
	}
	return copyProfileInvestigation(row), nil
}

func (m *MemStore) ListProfileInvestigations(_ context.Context, accountID, appID string) ([]api.ProfileInvestigation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return nil, ErrNotFound
	}
	out := []api.ProfileInvestigation{}
	for _, row := range m.profileInvestigations {
		if row.AppID == appID {
			out = append(out, copyProfileInvestigation(row))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (m *MemStore) SaveProfileInvestigation(_ context.Context, accountID, appID, id string, req api.SaveProfileInvestigationRequest) (api.ProfileInvestigation, error) {
	if err := ValidateProfileInvestigation(req); err != nil {
		return api.ProfileInvestigation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileInvestigation{}, ErrNotFound
	}
	for _, q := range []api.ProfileQuery{req.Investigation.Baseline, req.Investigation.Candidate} {
		dep, ok := m.deployments[q.DeploymentID]
		// Existing selections can outlive deleted deployment records.
		old := m.profileInvestigations[id]
		unchanged := sameInvestigationSelection(q, old.Investigation.Baseline) || sameInvestigationSelection(q, old.Investigation.Candidate)
		if (!ok || dep.AppID != appID) && !unchanged {
			return api.ProfileInvestigation{}, ErrNotFound
		}
	}
	row := api.ProfileInvestigation{}
	if id == "" {
		if *req.ExpectedRevision != 0 {
			return row, ErrProfileInvestigationRevision
		}
		count := 0
		for _, saved := range m.profileInvestigations {
			if saved.AppID == appID {
				count++
			}
		}
		if count >= api.ProfileInvestigationMaxPerApp {
			return row, ErrProfileInvestigationQuota
		}
		row.ID, row.AppID, row.CreatedAt = uuid.NewString(), appID, time.Now().UTC()
	} else {
		var ok bool
		row, ok = m.profileInvestigations[id]
		if !ok || row.AppID != appID {
			return api.ProfileInvestigation{}, ErrNotFound
		}
		if row.Revision != *req.ExpectedRevision {
			return api.ProfileInvestigation{}, ErrProfileInvestigationRevision
		}
	}
	row.Revision++
	row.Investigation = req.Investigation
	if req.InitialAssessment != nil {
		row.Assessment = req.InitialAssessment
	}
	row.UpdatedAt = time.Now().UTC()
	if m.profileInvestigations == nil {
		m.profileInvestigations = map[string]api.ProfileInvestigation{}
	}
	m.profileInvestigations[row.ID] = copyProfileInvestigation(row)
	return copyProfileInvestigation(row), nil
}

func sameInvestigationSelection(a, b api.ProfileQuery) bool {
	return a.Route == b.Route && a.DeploymentID == b.DeploymentID && a.Runtime == b.Runtime && a.Start.Equal(b.Start) && a.End.Equal(b.End)
}

func (m *MemStore) DeleteProfileInvestigation(_ context.Context, accountID, appID, id string, revision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileInvestigations[id]
	if !ok || row.AppID != appID || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return ErrNotFound
	}
	if row.Revision != revision {
		return ErrProfileInvestigationRevision
	}
	delete(m.profileInvestigations, id)
	return nil
}

func (m *MemStore) deleteProfileInvestigationsLocked(appID string) {
	delete(m.profileDeploymentPolicies, appID)
	for id, row := range m.profileDeploymentChecks {
		if row.check.AppID == appID {
			delete(m.profileDeploymentChecks, id)
		}
	}
	for id, row := range m.profileCanaryChecks {
		if row.appID == appID {
			delete(m.profileCanaryChecks, id)
		}
	}
	for id, row := range m.profileInvestigations {
		if row.AppID == appID {
			delete(m.profileInvestigations, id)
		}
	}
}

func (m *MemStore) SaveProfileRegressionAssessment(_ context.Context, accountID, appID, id string, revision int64, assessment api.ProfileRegressionAssessment) (api.ProfileInvestigation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileInvestigations[id]
	if !ok || row.AppID != appID || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileInvestigation{}, ErrNotFound
	}
	if _, err := validateProfileAssessment(row, revision, assessment); err != nil {
		return api.ProfileInvestigation{}, err
	}
	row.Revision++
	row.Assessment = &assessment
	row.UpdatedAt = time.Now().UTC()
	m.profileInvestigations[id] = copyProfileInvestigation(row)
	return copyProfileInvestigation(row), nil
}
