package state

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ CheckedRollbackStore = (*MemStore)(nil)

func cloneRollback(r api.RollbackOperation) api.RollbackOperation {
	raw, _ := json.Marshal(r)
	var out api.RollbackOperation
	_ = json.Unmarshal(raw, &out)
	return out
}
func (m *MemStore) checkedRollbackForTargetLocked(id string) (api.RollbackOperation, bool) {
	for _, r := range m.checkedRollbacks {
		if r.TargetDeploymentID == id && !rollbackTerminal(r) {
			return r, true
		}
	}
	return api.RollbackOperation{}, false
}
func (m *MemStore) CheckedRollbackForTarget(_ context.Context, id string) (api.RollbackOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.checkedRollbackForTargetLocked(id)
	if !ok {
		return r, ErrNotFound
	}
	return cloneRollback(r), nil
}
func (m *MemStore) GetCheckedRollback(_ context.Context, acct, app, id string) (api.RollbackOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.checkedRollbacks[id]
	a := m.apps[app]
	if !ok || r.AppID != app || a.AccountID != acct || a.Status == AppDeleted {
		return api.RollbackOperation{}, ErrNotFound
	}
	return cloneRollback(r), nil
}
func (m *MemStore) ListPendingCheckedRollbacks(context.Context) ([]api.RollbackOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.RollbackOperation{}
	for _, r := range m.checkedRollbacks {
		if !rollbackTerminal(r) {
			out = append(out, cloneRollback(r))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	if len(out) > api.ServiceBindingCheckBatchSize {
		out = out[:api.ServiceBindingCheckBatchSize]
	}
	return out, nil
}
func (m *MemStore) rollbackRowsLocked(r api.RollbackOperation) []Deployment {
	rows := []Deployment{}
	for _, d := range m.deployments {
		if d.AppID == r.AppID && normalizedDeploymentScope(d.Scope) == r.Scope {
			rows = append(rows, d)
		}
	}
	return rows
}
func (m *MemStore) CreateCheckedRollback(_ context.Context, acct, app, target, current, reason string) (api.RollbackOperation, error) {
	if err := validateCheckedRollbackArguments(acct, app, target, current, reason); err != nil {
		return api.RollbackOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createCheckedRollbackLocked(acct, app, target, current, reason, "")
}
func (m *MemStore) createCheckedRollbackLocked(acct, app, target, current, reason, id string) (api.RollbackOperation, error) {
	a, ok := m.apps[app]
	if !ok || a.AccountID != acct || a.Status == AppDeleted {
		return api.RollbackOperation{}, ErrNotFound
	}
	d, ok := m.deployments[target]
	if !ok || d.AppID != app {
		return api.RollbackOperation{}, ErrNoRollbackTarget
	}
	r := newCheckedRollback(app, d.Scope, target, current, reason, a.Manifest.ExecutionMode == api.ExecutionModeService)
	if id != "" {
		r.ID = id
	}
	if !rollbackPairValid(r, d, m.deployments[current], m.rollbackRowsLocked(r)) {
		return r, ErrCheckedRollbackChanged
	}
	for _, existing := range m.checkedRollbacks {
		if existing.AppID == app && existing.Scope == r.Scope && !rollbackTerminal(existing) {
			return r, ErrConflict
		}
	}
	d, err := m.prepareDeploymentRollbackLocked(app, target)
	if err != nil {
		return r, err
	}
	d.TrafficPercentExplicit = true
	d.APIHostingReceipt = nil
	d.ServiceRolloutHandoff = rollbackHandoff(r)
	if r.Service {
		d.RolloutState = "rolling_out"
	}
	m.putDeploymentLocked(target, d)
	if m.checkedRollbacks == nil {
		m.checkedRollbacks = map[string]api.RollbackOperation{}
	}
	m.checkedRollbacks[r.ID] = r
	return cloneRollback(r), nil
}
func (m *MemStore) markCheckedRollbackReadyLocked(ctx context.Context, d Deployment, r api.RollbackOperation) error {
	if d.Status != DeploySnapshotting || d.TrafficPercent != 0 {
		return ErrCheckedRollbackChanged
	}
	if !rollbackPairValid(r, d, m.deployments[r.CurrentDeploymentID], m.rollbackRowsLocked(r)) {
		return ErrCheckedRollbackChanged
	}
	if _, err := m.checkDeploymentDependenciesLocked(d.ID, time.Now().UTC(), false); err != nil {
		return err
	}
	d.Status = DeployLive
	if r.Service {
		now := time.Now().UTC()
		d.RolloutStartedAt = &now
	}
	if err := m.checkServiceCapacityDeploymentLocked(d); err != nil {
		return err
	}
	if err := m.captureAndStoreDeploymentSnapshotsLocked(ctx, d, true); err != nil {
		return err
	}
	m.putDeploymentLocked(d.ID, d)
	r.Status = "ready"
	r.UpdatedAt = time.Now().UTC()
	m.checkedRollbacks[r.ID] = r
	return nil
}
func (m *MemStore) CommitCheckedRollback(ctx context.Context, snapshot api.RollbackOperation) (api.RollbackOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.checkedRollbacks[snapshot.ID]
	if !ok || r.AppID != snapshot.AppID || r.TargetDeploymentID != snapshot.TargetDeploymentID || r.CurrentDeploymentID != snapshot.CurrentDeploymentID || r.Status != "ready" && r.Status != "blocked" {
		return r, ErrCheckedRollbackChanged
	}
	d := m.deployments[r.TargetDeploymentID]
	if d.Status != DeployLive || d.TrafficPercent != 0 || !rollbackPairValid(r, d, m.deployments[r.CurrentDeploymentID], m.rollbackRowsLocked(r)) {
		return r, ErrCheckedRollbackChanged
	}
	ctx = withCheckedRollback(ctx, r.ID)
	if err := m.checkBindingReleaseTrafficLocked(ctx, map[string]int{d.ID: 100}); err != nil {
		return r, err
	}
	if r.Service {
		var err error
		d, err = m.beginServiceRolloutCutoverLocked(WithServiceRolloutBindingRequest(ctx, r.ID), d.ID)
		if err != nil {
			return r, err
		}
		r.Status = "routing"
		r.AuditID = d.ServiceRolloutHandoff.BindingsCheck.AuditID
	} else {
		accountID := uuid.MustParse(m.apps[r.AppID].AccountID)
		data, _ := json.Marshal(map[string]any{"request_id": r.ID, "target_deployment_id": r.TargetDeploymentID, "current_deployment_id": r.CurrentDeploymentID, "phase": "complete", "reason": r.Reason, "binding_fences": bindingReleaseFences(ctx)})
		auditID, err := m.appendDeploymentAuditLocked(DeploymentAudit{DeploymentID: uuid.MustParse(d.ID), AccountID: &accountID, Kind: DeployRolledBack, Actor: "apid:checked_rollback", At: time.Now().UTC(), Data: data})
		if err != nil {
			return r, err
		}
		now := time.Now().UTC()
		ttl := m.apps[r.AppID].Manifest.RevisionPinTTLSeconds
		for id, other := range m.deployments {
			if id == d.ID || other.AppID != r.AppID || normalizedDeploymentScope(other.Scope) != r.Scope || other.Status != DeployLive {
				continue
			}
			if ttl > 0 && ttl <= api.RevisionPinMaxTTLSeconds && other.TrafficPercent > 0 {
				if _, ok := m.revisionPins[id]; !ok {
					m.revisionPins[id] = now.Add(time.Duration(ttl) * time.Second)
				}
			}
			other.TrafficPercent = 0
			if expiry, ok := m.revisionPins[id]; !ok || !now.Before(expiry) {
				if !m.deploymentInUsableReleaseLocked(id) {
					other.Status = DeploySuperseded
				}
			}
			m.putDeploymentLocked(id, other)
		}
		d.TrafficPercent = 100
		d.RolloutState = "complete"
		d.RolloutCompletedAt = &now
		m.putDeploymentLocked(d.ID, d)
		r.Status = "complete"
		r.CompletedAt = &now
		r.AuditID = strconv.FormatInt(auditID, 10)
	}
	r.Code = ""
	r.Blockers = nil
	r.UpdatedAt = time.Now().UTC()
	m.checkedRollbacks[r.ID] = r
	return cloneRollback(r), nil
}
func (m *MemStore) UpdateCheckedRollback(_ context.Context, snapshot api.RollbackOperation, status, code string, blockers []api.BindingCheckFinding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.checkedRollbacks[snapshot.ID]
	if !ok || r.AppID != snapshot.AppID || r.Status != snapshot.Status || rollbackTerminal(r) || !validRollbackStatusUpdate(r, status) {
		return ErrCheckedRollbackChanged
	}
	d := m.deployments[r.TargetDeploymentID]
	if status == "complete" && (r.Status != "routing" || d.Status != DeployLive || d.TrafficPercent != 100 || d.RolloutState != "complete" || d.ServiceRolloutHandoff.Phase != "complete" || d.ServiceRolloutHandoff.Action != "promote" || d.ServiceRolloutHandoff.PredecessorDeploymentID != r.CurrentDeploymentID || d.ServiceRolloutHandoff.BindingsCheck == nil || d.ServiceRolloutHandoff.BindingsCheck.RequestID != r.ID || d.ServiceRolloutHandoff.BindingsCheck.AuditID != r.AuditID) {
		return ErrCheckedRollbackChanged
	}
	if status == "failed" && d.TrafficPercent == 0 {
		d.Status = DeploySuperseded
		d.RolloutState = "pending"
		m.putDeploymentLocked(d.ID, d)
	}
	m.checkedRollbacks[r.ID] = blockedRollback(r, status, code, blockers)
	return nil
}
