package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) savePlaybookEvidenceWatch(ctx context.Context, identity workflowPlaybookProgressRecord, req api.OperationWorkflowPlaybookEvidenceWatchRequest, w api.OperationWorkflowPlaybookEvidenceWatch, operator bool) (api.OperationWorkflowPlaybookEvidenceWatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return w, err
	}
	data := m.operationMemoryLocked()
	r, ok := data.readinessSubscriptions[req.SubscriptionID]
	tenant := identity.Tenant
	if operator {
		tenant = ""
	}
	if !ok || !readinessRecordMatches(r, identity.Account, tenant, identity.App, identity.Scope, operator) {
		return w, ErrNotFound
	}
	p, ok := data.playbookProgress[identity.Progress.ID]
	if !ok || p.key() != identity.key() {
		return w, ErrNotFound
	}
	if p.Progress.Revision != req.ProgressRevision || req.Enabled && !workflowPlaybookProgressActive(p.Progress) {
		return w, ErrConflict
	}
	r = cloneReadinessRecord(r)
	out, err := configurePlaybookEvidenceWatchRecord(&r, identity, req, w)
	if err != nil {
		return out, err
	}
	data.readinessSubscriptions[r.Subscription.ID] = cloneReadinessRecord(r)
	return out, nil
}
