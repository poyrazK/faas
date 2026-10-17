package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"maps"
	"time"
)

func (m *MemStore) graphVerificationLocked(account, project, environment, id string) (graphVerificationRecord, error) {
	r, ok := m.graphVerificationRuns[id]
	if !ok || r.AccountID != account || r.ProjectID != project || r.Report.Environment != environment {
		return r, ErrNotFound
	}
	return cloneGraphVerification(r), nil
}
func (m *MemStore) checkGraphVerificationLocked(account, id string, captures ...map[string][]VerificationRunAdmission) error {
	r, ok := m.graphVerificationRuns[id]
	if !ok || r.AccountID != account {
		return ErrNotFound
	}
	if err := graphVerificationActive(r.Report); err != nil {
		return err
	}
	snapshot, err := m.releaseStageSnapshotLocked(account, r.ProjectID, r.Report.Environment, r.Report.Manifest.RollbackReleaseID)
	if err != nil {
		return err
	}
	if err = validateGraphVerificationSnapshot(snapshot, r.Report.Manifest); err != nil {
		return err
	}
	for _, child := range r.Report.Members {
		stored, err := m.verificationRunLocked(account, child.Policy.AppID, child.ID)
		if err != nil {
			return err
		}
		if stored.Report.GraphRunID != id || !graphMemberAdmissionsMatch(stored, child.App, captures, len(r.Report.Members)) {
			return ErrConflict
		}
		if err = graphMemberReviewActive(stored.Report); err != nil {
			return err
		}
		if err = m.checkVerificationRunAuthorityLocked(account, child.Policy.AppID, stored); err != nil {
			return err
		}
	}
	return nil
}
func (m *MemStore) CreateProjectBindingVerificationRun(_ context.Context, account, project string, in GraphVerificationCreate) (api.ProjectBindingVerificationRun, error) {
	if err := validateGraphVerificationCreate(account, in); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	in.Report = cloneGraphVerification(graphVerificationRecord{Report: in.Report}).Report
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.graphVerificationRuns[in.Report.ID]; ok {
		if old.AccountID != account || old.ProjectID != project || old.Report.Environment != in.Report.Environment {
			return api.ProjectBindingVerificationRun{}, ErrNotFound
		}
		if api.BindingVerificationConcurrency(old.Report.MaxConcurrency) != api.BindingVerificationConcurrency(in.Report.MaxConcurrency) || old.Report.ReviewDigest != in.Report.ReviewDigest {
			return api.ProjectBindingVerificationRun{}, ErrConflict
		}
		return publicGraphVerification(old), nil
	}
	snapshot, err := m.releaseStageSnapshotLocked(account, project, in.Report.Environment, in.Report.Manifest.RollbackReleaseID)
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	if err = validateGraphVerificationSnapshot(snapshot, in.Report.Manifest); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	active := 0
	for _, old := range m.graphVerificationRuns {
		if old.ProjectID == project && old.Report.Environment == in.Report.Environment && old.Report.Status == "pending" && old.Report.ExpiresAt.After(time.Now()) {
			active++
		}
	}
	if active >= api.ProjectBindingVerificationRunMaxActive {
		return api.ProjectBindingVerificationRun{}, &VerificationRunCapacityError{Limit: api.ProjectBindingVerificationRunMaxActive, Observed: active}
	}
	// Member creation only writes receipts. Restore their map if any member fails.
	original := m.bindingVerificationRuns
	m.bindingVerificationRuns = maps.Clone(original)
	for _, slug := range graphVerificationMemberNames(in.Report) {
		child := in.Members[slug]
		if _, exists := m.bindingVerificationRuns[child.Report.ID]; exists {
			m.bindingVerificationRuns = original
			return api.ProjectBindingVerificationRun{}, ErrConflict
		}
		report, err := m.createBindingVerificationRunLocked(account, child.Report.Policy.AppID, child)
		if err != nil {
			m.bindingVerificationRuns = original
			return api.ProjectBindingVerificationRun{}, err
		}
		in.Report.Members[slug] = report
	}
	if m.graphVerificationRuns == nil {
		m.graphVerificationRuns = map[string]graphVerificationRecord{}
	}
	in.Report.Status = "pending"
	r := cloneGraphVerification(graphVerificationRecord{AccountID: account, ProjectID: project, Report: in.Report})
	m.graphVerificationRuns[r.Report.ID] = r
	for id, old := range m.graphVerificationRuns {
		if old.ProjectID == project && old.Report.ExpiresAt.Before(time.Now().Add(-24*time.Hour)) {
			for _, child := range old.Report.Members {
				delete(m.bindingVerificationRuns, child.ID)
			}
			delete(m.graphVerificationRuns, id)
		}
	}
	return publicGraphVerification(r), nil
}
func (m *MemStore) GetProjectBindingVerificationRun(_ context.Context, account, project, environment, id string) (api.ProjectBindingVerificationRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.graphVerificationLocked(account, project, environment, id)
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	return publicGraphVerification(r), nil
}
func (m *MemStore) CheckProjectBindingVerificationRun(_ context.Context, account, project, environment, id string, captures ...map[string][]VerificationRunAdmission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.graphVerificationLocked(account, project, environment, id); err != nil {
		return err
	}
	return m.checkGraphVerificationLocked(account, id, captures...)
}
func (m *MemStore) SetProjectBindingVerificationRunStatus(_ context.Context, account, project, environment, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.graphVerificationLocked(account, project, environment, id)
	if err != nil {
		return err
	}
	if status != "cancelled" && status != "passed" {
		return ErrInvalidArgument
	}
	if status == "passed" {
		if err = m.checkGraphVerificationLocked(account, id); err != nil {
			return err
		}
		for _, child := range r.Report.Members {
			if m.bindingVerificationRuns[child.ID].Report.Status != "passed" {
				return ErrConflict
			}
		}
	}
	r.Report.Status = status
	m.graphVerificationRuns[id] = r
	return nil
}
