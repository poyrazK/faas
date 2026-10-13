package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) savePlaybookEvidenceWatch(ctx context.Context, identity workflowPlaybookProgressRecord, req api.OperationWorkflowPlaybookEvidenceWatchRequest, w api.OperationWorkflowPlaybookEvidenceWatch, operator bool) (api.OperationWorkflowPlaybookEvidenceWatch, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return w, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	a, _ := operationUUID(identity.Account)
	app, _ := operationUUID(identity.App)
	id, _ := operationUUID(req.SubscriptionID)
	if _, err = q.LockCustomerOperationAccount(ctx, tx, a); err != nil {
		return w, mapErr(err)
	}
	tenant := identity.Tenant
	if operator {
		tenant = ""
	}
	row, err := q.LockReadinessSubscription(ctx, tx, sqlc.LockReadinessSubscriptionParams{ID: id, AccountID: a, AppID: app, Tenant: tenant, Scope: identity.Scope, Operator: operator})
	if err != nil {
		return w, mapErr(err)
	}
	r, err := readinessPGRecord(row)
	if err != nil {
		return w, err
	}
	progress, err := readPGPlaybookProgress(ctx, tx, identity)
	if err != nil {
		return w, err
	}
	found := false
	for _, p := range progress {
		if p.ID == identity.Progress.ID {
			found = true
			if p.Revision != req.ProgressRevision || req.Enabled && !workflowPlaybookProgressActive(p) {
				return w, ErrConflict
			}
			break
		}
	}
	if !found {
		return w, ErrNotFound
	}
	out, err := configurePlaybookEvidenceWatchRecord(&r, identity, req, w)
	if err != nil {
		return out, err
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return out, err
	}
	if _, err = q.SaveReadinessSubscriptionWatches(ctx, tx, sqlc.SaveReadinessSubscriptionWatchesParams{ID: id, Payload: payload, NextEvaluationAt: readinessPGTime(r.NextEvaluation)}); err != nil {
		return out, mapErr(err)
	}
	return out, tx.Commit(ctx)
}
