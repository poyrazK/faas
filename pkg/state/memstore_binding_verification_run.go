package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) verificationRunLocked(account, app, id string) (verificationRunRecord, error) {
	r, ok := m.bindingVerificationRuns[id]
	if !ok || r.Report.Policy.AppID != app || m.apps[app].AccountID != account {
		return r, ErrNotFound
	}
	return cloneVerificationRecord(r), nil
}
func (m *MemStore) checkVerificationRunLocked(account, app string, r verificationRunRecord) error {
	if err := verificationRunActive(r.Report); err != nil {
		return err
	}
	if r.Report.GraphRunID != "" {
		if err := m.checkGraphVerificationLocked(account, r.Report.GraphRunID); err != nil {
			return err
		}
	}
	return m.checkVerificationRunAuthorityLocked(account, app, r)
}
func (m *MemStore) checkVerificationRunAuthorityLocked(account, app string, r verificationRunRecord) error {
	a := m.apps[app]
	d := m.deployments[r.Report.DeploymentID]
	p := m.bindingReleasePolicyLocked(app, r.Report.Scope)
	if a.AccountID != account || a.Status == AppDeleted || d.AppID != app || d.Status != DeployLive || d.DeletedAt != nil || d.RootfsKey == "" || d.ImageDigest == "" || verificationArtifact(d.RootfsKey, d.ImageDigest, d.OverrideEnv) != r.Artifact || p.Revision != r.Report.Policy.Revision || p.Mode != "enforce" || !m.runtimeConfigChangedAt[app].Equal(*r.Report.Inventory.RuntimeFreshness.ConfigChangedAt) {
		return ErrConflict
	}
	for _, admission := range r.Admissions {
		if admission.Queue != nil {
			snap, err := m.queueCanarySnapshotLocked(account, app, r.Report.DeploymentID, admission.Queue.Name)
			if err != nil {
				return err
			}
			if !sameQueueCanarySnapshot(snap, *admission.Queue) {
				return ErrConflict
			}
		}
	}
	return nil
}
func (m *MemStore) CreateBindingVerificationRun(_ context.Context, account, app string, in VerificationRunCreate) (api.BindingVerificationRun, error) {
	if err := validateVerificationRunCreate(account, app, in); err != nil {
		return api.BindingVerificationRun{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createBindingVerificationRunLocked(account, app, in)
}
func (m *MemStore) createBindingVerificationRunLocked(account, app string, in VerificationRunCreate) (api.BindingVerificationRun, error) {
	if old, ok := m.bindingVerificationRuns[in.Report.ID]; ok {
		if old.Report.Policy.AppID != app || m.apps[app].AccountID != account {
			return api.BindingVerificationRun{}, ErrNotFound
		}
		if api.BindingVerificationConcurrency(old.Report.MaxConcurrency) != api.BindingVerificationConcurrency(in.Report.MaxConcurrency) || old.Report.ReviewDigest != in.Report.ReviewDigest || old.Report.QueuePublish != in.Report.QueuePublish || old.Report.ObjectStorageWrite != in.Report.ObjectStorageWrite {
			return api.BindingVerificationRun{}, ErrConflict
		}
		return publicVerificationRun(old), nil
	}
	active := 0
	for id, r := range m.bindingVerificationRuns {
		if r.Report.Policy.AppID == app {
			if r.Report.GraphRunID == "" && r.Report.ExpiresAt.Before(time.Now().Add(-24*time.Hour)) {
				delete(m.bindingVerificationRuns, id)
			} else if verificationRunActive(r.Report) == nil {
				active++
			}
		}
	}
	if active >= api.BindingVerificationRunMaxActive {
		return api.BindingVerificationRun{}, &VerificationRunCapacityError{Limit: api.BindingVerificationRunMaxActive, Observed: active}
	}
	d := m.deployments[in.Report.DeploymentID]
	r := cloneVerificationRecord(verificationRunRecord{Report: in.Report, Admissions: in.Admissions, Artifact: verificationArtifact(d.RootfsKey, d.ImageDigest, d.OverrideEnv)})
	r.Report.Status = "pending"
	if err := m.checkVerificationRunAuthorityLocked(account, app, r); err != nil {
		return api.BindingVerificationRun{}, err
	}
	for i, step := range r.Report.Steps {
		if step.Plan.Type == api.BindingTypeQueue && step.Plan.RequiredEvidence == api.QueueEvidenceHandlerCompletion {
			snap, err := m.queueCanarySnapshotLocked(account, app, r.Report.DeploymentID, step.Plan.Binding)
			if err != nil {
				return api.BindingVerificationRun{}, err
			}
			r.Admissions[i].Queue = &snap
		}
	}
	if m.bindingVerificationRuns == nil {
		m.bindingVerificationRuns = map[string]verificationRunRecord{}
	}
	m.bindingVerificationRuns[r.Report.ID] = cloneVerificationRecord(r)
	return publicVerificationRun(r), nil
}
func (m *MemStore) GetBindingVerificationRun(ctx context.Context, account, app, id string) (api.BindingVerificationRun, error) {
	m.mu.Lock()
	r, err := m.verificationRunLocked(account, app, id)
	m.mu.Unlock()
	if err != nil {
		return api.BindingVerificationRun{}, err
	}
	return observeVerificationRun(ctx, m, m, account, app, publicVerificationRun(r))
}
func (m *MemStore) CheckBindingVerificationRun(_ context.Context, account, app, id string, admissions ...[]VerificationRunAdmission) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.verificationRunLocked(account, app, id)
	if err != nil {
		return err
	}
	if !verificationRunAdmissionsMatch(r, admissions) {
		return ErrConflict
	}
	return m.checkVerificationRunLocked(account, app, r)
}
func (m *MemStore) SetBindingVerificationRunStatus(_ context.Context, account, app, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.verificationRunLocked(account, app, id)
	if err != nil {
		return err
	}
	if status != "passed" && status != "cancelled" {
		return ErrInvalidArgument
	}
	if status == "passed" {
		if r.Report.Status == "passed" {
			return nil
		}
		if err = m.checkVerificationRunLocked(account, app, r); err != nil {
			return err
		}
	}
	r.Report.Status = status
	m.bindingVerificationRuns[id] = r
	return nil
}
func (m *MemStore) AdmitBindingVerificationRunStep(_ context.Context, account, app, id string, index int) (api.BindingVerificationRunStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.verificationRunLocked(account, app, id)
	if err != nil {
		return api.BindingVerificationRunStep{}, err
	}
	if err = m.checkVerificationRunLocked(account, app, r); err != nil {
		return api.BindingVerificationRunStep{}, err
	}
	if index < 0 || index >= len(r.Report.Steps) {
		return api.BindingVerificationRunStep{}, ErrInvalidArgument
	}
	step := r.Report.Steps[index]
	if step.ResourceID != "" {
		return step, nil
	}
	budget, err := m.verificationBudgetLocked(account, r)
	if err != nil {
		return step, err
	}
	if budget.Full {
		return step, &VerificationRunBudgetError{Limit: budget.MaxConcurrency, Observed: budget.ActiveResources}
	}
	admission := r.Admissions[index]
	switch step.Plan.Action {
	case "probe":
		params := *admission.Task
		params.CreatedAt = time.Now().UTC()
		resolved, err := resolveCreateAppTask(params)
		if err != nil {
			return step, err
		}
		task, err := m.createAppTaskLocked(resolved)
		if err != nil {
			return step, err
		}
		task.StartDeadlineAt = &r.Report.ExpiresAt
		m.appTasks[task.ID] = task
		step.ResourceID, step.ResourceKind, step.Owned, step.Status = task.ID, "task", true, string(task.Status)
	case "queue-canary":
		snap, err := m.queueCanarySnapshotLocked(account, app, r.Report.DeploymentID, step.Plan.Binding)
		if err != nil {
			return step, err
		}
		if admission.Queue == nil || !sameQueueCanarySnapshot(snap, *admission.Queue) {
			return step, ErrConflict
		}
		ttl := int(time.Until(r.Report.ExpiresAt) / time.Second)
		if ttl < 1 {
			return step, ErrConflict
		}
		canary, err := m.createQueueBindingCanaryLocked(account, app, api.CreateQueueBindingCanaryRequest{DeploymentID: r.Report.DeploymentID, Binding: step.Plan.Binding, TTLSeconds: ttl, PublishConsent: true})
		if err != nil {
			return step, err
		}
		step.ResourceID, step.ResourceKind, step.Owned, step.Status = canary.ID, "queue", true, canary.Status
	default:
		return step, ErrConflict
	}
	r.Report.Steps[index] = step
	m.bindingVerificationRuns[id] = r
	return step, nil
}
