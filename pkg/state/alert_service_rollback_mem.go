package state

import (
	"context"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ServiceAlertRollbackStore = (*MemStore)(nil)

func (m *MemStore) requestServiceAlertRollbackLocked(ctx context.Context, r api.AlertRollback) (api.AlertRollback, error) {
	target, rows, err := m.serviceRolloutTargetLocked(r.CandidateDeploymentID)
	if err != nil {
		return r, err
	}
	target.ServiceRolloutHandoff.PredecessorDeploymentID = r.PredecessorDeploymentID
	previous, found := previousMemServiceRolloutRow(target, rows)
	if !found || previous.id != r.PredecessorDeploymentID {
		return r, ErrAlertRollbackChanged
	}
	r, target, err = queueAlertServiceRollback(r, target)
	if err != nil {
		return r, err
	}
	auditID, err := m.appendDeploymentAuditLocked(alertServiceRollbackAudit(ctx, r, "requested"))
	if err != nil {
		return r, err
	}
	r.AuditID = strconv.FormatInt(auditID, 10)
	m.deployments[target.ID], m.alertRollbacks[r.ID] = target, r
	return cloneAlertRollback(r), nil
}

func (m *MemStore) RefreshServiceAlertRollback(ctx context.Context, snapshot api.AlertRollback) (api.AlertRollback, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRollbacks[snapshot.ID]
	if !ok {
		return r, ErrNotFound
	}
	if alertRollbackTerminal(r) {
		return cloneAlertRollback(r), nil
	}
	if !r.Service || r.ServiceRequestID == "" {
		return r, ErrAlertRollbackChanged
	}
	r = projectAlertServiceRollback(r, m.deployments[r.CandidateDeploymentID], m.deployments[r.PredecessorDeploymentID])
	if r.Status == "complete" {
		auditID, err := m.appendDeploymentAuditLocked(alertServiceRollbackAudit(ctx, r, "complete"))
		if err != nil {
			return r, err
		}
		r = finishAlertServiceRollback(r, auditID)
	}
	m.alertRollbacks[r.ID] = r
	return cloneAlertRollback(r), nil
}
