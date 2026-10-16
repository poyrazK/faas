package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readinessPGTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func readinessPGRecord(row sqlc.CustomerWorkflowReadinessSubscription) (readinessSubscriptionRecord, error) {
	var r readinessSubscriptionRecord
	if err := json.Unmarshal(row.Payload, &r); err != nil {
		return r, err
	}
	r.Account = uuid.UUID(row.AccountID.Bytes).String()
	r.Operator = row.Operator
	if row.TenantID.Valid {
		r.Tenant = uuid.UUID(row.TenantID.Bytes).String()
	}
	if row.LeaseToken.Valid {
		r.Lease = uuid.UUID(row.LeaseToken.Bytes).String()
	}
	r.NextEvaluation = row.NextEvaluationAt.Time
	r.LeaseUntil = row.LeaseUntil.Time
	return r, nil
}
func (s *PgStore) insertReadinessSubscription(ctx context.Context, r readinessSubscriptionRecord) error {
	account, _ := operationUUID(r.Account)
	app, _ := operationUUID(r.Subscription.AppID)
	id, _ := operationUUID(r.Subscription.ID)
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockCustomerOperationAccount(ctx, tx, account); err != nil {
		return mapErr(err)
	}
	count, err := q.CountReadinessSubscriptions(ctx, tx, sqlc.CountReadinessSubscriptionsParams{AccountID: account, AppID: app})
	if err != nil {
		return mapErr(err)
	}
	if count >= api.OperationWorkflowReadinessSubscriptionsPerAppMax {
		return NewOperationLimitError("readiness_subscriptions_per_app", int64(api.OperationWorkflowReadinessSubscriptionsPerAppMax), count+1)
	}
	n, err := q.InsertReadinessSubscription(ctx, tx, sqlc.InsertReadinessSubscriptionParams{ID: id, AccountID: account, AppID: app, Tenant: r.Tenant, Operator: r.Operator, Scope: r.Subscription.Scope, Payload: raw, CreatedAt: readinessPGTime(r.Subscription.CreatedAt), NextEvaluationAt: readinessPGTime(r.NextEvaluation)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
func (s *PgStore) listReadinessSubscriptions(ctx context.Context, account, tenant, app, scope string, operator bool) ([]api.OperationWorkflowReadinessSubscription, error) {
	a, _ := operationUUID(account)
	p, _ := operationUUID(app)
	raw, err := sqlc.New().ListReadinessSubscriptions(ctx, s.pool, sqlc.ListReadinessSubscriptionsParams{AccountID: a, AppID: p, Tenant: canonicalReadinessTenant(tenant), Scope: scope, Operator: operator, Limit: api.OperationWorkflowReadinessSubscriptionsPerAppMax})
	if err != nil {
		return nil, mapErr(err)
	}
	out := []api.OperationWorkflowReadinessSubscription{}
	for _, payload := range raw {
		var r readinessSubscriptionRecord
		if err = json.Unmarshal(payload, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Subscription)
	}
	return out, nil
}
func canonicalReadinessTenant(t string) string {
	if t == "" {
		return ""
	}
	return uuid.MustParse(t).String()
}
func (s *PgStore) deleteReadinessSubscription(ctx context.Context, account, tenant, app, scope, id string, operator bool) error {
	a, _ := operationUUID(account)
	p, _ := operationUUID(app)
	i, _ := operationUUID(id)
	n, err := sqlc.New().DeleteReadinessSubscription(ctx, s.pool, sqlc.DeleteReadinessSubscriptionParams{ID: i, AccountID: a, AppID: p, Tenant: canonicalReadinessTenant(tenant), Scope: scope, Operator: operator})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PgStore) readReadinessInbox(ctx context.Context, account, tenant, app, scope, id string, after int64, limit int, operator bool) (api.OperationWorkflowReadinessInboxResponse, error) {
	out := api.OperationWorkflowReadinessInboxResponse{}
	a, _ := operationUUID(account)
	p, _ := operationUUID(app)
	i, _ := operationUUID(id)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.GetReadinessSubscription(ctx, tx, sqlc.GetReadinessSubscriptionParams{ID: i, AccountID: a, AppID: p, Tenant: canonicalReadinessTenant(tenant), Scope: scope, Operator: operator})
	if err != nil {
		return out, mapErr(err)
	}
	record, err := readinessPGRecord(row)
	if err != nil {
		return out, err
	}
	raw, err := q.ListReadinessNotifications(ctx, tx, sqlc.ListReadinessNotificationsParams{SubscriptionID: i, Sequence: after, Limit: int32(limit)})
	if err != nil {
		return out, mapErr(err)
	}
	items := []api.OperationWorkflowReadinessNotification{}
	for _, payload := range raw {
		var item api.OperationWorkflowReadinessNotification
		if err = json.Unmarshal(payload, &item); err != nil {
			return out, err
		}
		items = append(items, item)
	}
	out = readinessInboxPage(record.Subscription, items, after)
	return out, tx.Commit(ctx)
}
func (s *PgStore) claimReadinessSubscriptions(ctx context.Context) ([]readinessSubscriptionRecord, error) {
	token, _ := operationUUID(uuid.NewString())
	now := time.Now().UTC()
	rows, err := sqlc.New().ClaimReadinessSubscriptions(ctx, s.pool, sqlc.ClaimReadinessSubscriptionsParams{Now: readinessPGTime(now), LeaseToken: token, LeaseUntil: readinessPGTime(now.Add(api.OperationWorkflowReadinessSubscriptionLease)), BatchLimit: api.OperationWorkflowReadinessSubscriptionBatchMax})
	if err != nil {
		return nil, mapErr(err)
	}
	out := []readinessSubscriptionRecord{}
	for _, row := range rows {
		r, err := readinessPGRecord(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *PgStore) finishReadinessSubscription(ctx context.Context, r readinessSubscriptionRecord, notifications []api.OperationWorkflowReadinessNotification, watchUpdates []readinessWatchUpdate) error {
	id, _ := operationUUID(r.Subscription.ID)
	lease, _ := operationUUID(r.Lease)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Match checklist writers' account-first lock order before locking the subscription.
	for _, u := range watchUpdates {
		if u.Playbook != nil || u.ResponseWatchID != "" {
			account, _ := operationUUID(r.Account)
			if _, err = q.LockCustomerOperationAccount(ctx, tx, account); err != nil {
				return mapErr(err)
			}
			break
		}
	}
	_, err = q.LockReadinessSubscriptionLease(ctx, tx, sqlc.LockReadinessSubscriptionLeaseParams{ID: id, LeaseToken: lease})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	for _, u := range watchUpdates {
		if u.Playbook != nil {
			identity := playbookEvidenceWatchIdentity(r, u.Playbook.Watch)
			rows, e := readPGPlaybookProgress(ctx, tx, identity)
			if e != nil {
				return e
			}
			var progress *api.OperationWorkflowPlaybookProgress
			for _, p := range rows {
				if p.ID == u.Playbook.Watch.ProgressID {
					row := p
					progress = &row
					break
				}
			}
			published, accepted := applyPlaybookEvidenceWatchUpdate(&r, *u.Playbook, progress)
			notifications = append(notifications, published...)
			if accepted {
				for _, watch := range r.Subscription.PlaybookEvidenceWatches {
					if watch.ID != u.Playbook.Watch.ID {
						continue
					}
					if err = syncPlaybookStepAssignmentsTx(ctx, tx, q, id, r.Subscription.Scope, watch, *progress); err != nil {
						return err
					}
					if err = syncPlaybookOutcomeEscalationTx(ctx, tx, q, id, r.Subscription.Scope, watch); err != nil {
						return err
					}
					break
				}
			}
			continue
		}
		payload, err := q.LockReadinessNotification(ctx, tx, sqlc.LockReadinessNotificationParams{SubscriptionID: id, Sequence: u.Sequence})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return mapErr(err)
		}
		var n api.OperationWorkflowReadinessNotification
		if err = json.Unmarshal(payload, &n); err != nil {
			return err
		}
		prior := cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: []api.OperationWorkflowReadinessNotification{n}}).Items[0]
		refreshed := false
		if u.ResponseWatchID != "" && n.PlaybookStep != nil && n.PlaybookEvidence != nil {
			rows, e := readPGPlaybookProgress(ctx, tx, playbookStepResponseIdentity(r, n))
			if e != nil {
				return e
			}
			n, refreshed = refreshPlaybookStepResponse(n, rows)
		}
		updated, published, ok := applyReadinessWatchUpdate(ctx, &r, n, u)
		if !ok && !refreshed {
			continue
		}
		updated = captureObligationReceipts(ctx, r.Subscription.ID, r.Subscription.Scope, prior, updated, "")
		payload, err = json.Marshal(updated)
		if err != nil {
			return err
		}
		if _, err = q.UpdateReadinessNotification(ctx, tx, sqlc.UpdateReadinessNotificationParams{SubscriptionID: id, Sequence: u.Sequence, Payload: payload}); err != nil {
			return mapErr(err)
		}
		if n.BusinessOutcomeWatch != nil && n.BusinessOutcomeWatch.Escalation != nil {
			if err = syncReadinessOutcomeEscalationTx(ctx, tx, q, id, r.Subscription.Scope, updated); err != nil {
				return err
			}
		}
		notifications = append(notifications, published...)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = q.FinishReadinessSubscription(ctx, tx, sqlc.FinishReadinessSubscriptionParams{ID: id, LeaseToken: lease, Payload: raw, NextEvaluationAt: readinessPGTime(r.NextEvaluation)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	for _, notification := range notifications {
		notification = captureObligationReceipts(ctx, r.Subscription.ID, r.Subscription.Scope, api.OperationWorkflowReadinessNotification{}, notification, "")
		payload, err := json.Marshal(notification)
		if err != nil {
			return err
		}
		if err = q.InsertReadinessNotification(ctx, tx, sqlc.InsertReadinessNotificationParams{SubscriptionID: id, Sequence: notification.Sequence, Payload: payload}); err != nil {
			return mapErr(err)
		}
	}
	if err = q.PruneReadinessNotifications(ctx, tx, sqlc.PruneReadinessNotificationsParams{SubscriptionID: id, Sequence: r.Subscription.LatestSequence - int64(api.OperationWorkflowReadinessInboxMax)}); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) updateReadinessNotification(ctx context.Context, account, tenant, app, scope, id string, sequence int64, actor string, req readinessCoordinationUpdate, operator bool) (api.OperationWorkflowReadinessNotification, error) {
	var out api.OperationWorkflowReadinessNotification
	a, _ := operationUUID(account)
	p, _ := operationUUID(app)
	i, _ := operationUUID(id)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.LockReadinessSubscription(ctx, tx, sqlc.LockReadinessSubscriptionParams{ID: i, AccountID: a, AppID: p, Tenant: canonicalReadinessTenant(tenant), Scope: scope, Operator: operator})
	if err != nil {
		return out, mapErr(err)
	}

	raw, err := q.LockReadinessNotification(ctx, tx, sqlc.LockReadinessNotificationParams{SubscriptionID: i, Sequence: sequence})
	if err != nil {
		return out, mapErr(err)
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	before := cloneReadinessInbox(api.OperationWorkflowReadinessInboxResponse{Items: []api.OperationWorkflowReadinessNotification{out}}).Items[0]
	out, err = applyReadinessCoordination(ctx, out, actor, req)
	if err != nil {
		return out, err
	}
	out = captureObligationReceipts(ctx, id, scope, before, out, actor)
	payload, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	_, err = q.UpdateReadinessNotification(ctx, tx, sqlc.UpdateReadinessNotificationParams{SubscriptionID: i, Sequence: sequence, Payload: payload})
	if err != nil {
		return out, mapErr(err)
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) readReadinessFollowUps(ctx context.Context, account, tenant, app, scope string, operator bool) ([]api.OperationWorkflowReadinessFollowUp, error) {
	a, _ := operationUUID(account)
	p, _ := operationUUID(app)
	rows, err := sqlc.New().ListReadinessFollowUps(ctx, s.pool, sqlc.ListReadinessFollowUpsParams{AccountID: a, AppID: p, Scope: scope, Operator: operator, Tenant: canonicalReadinessTenant(tenant), CandidateLimit: api.OperationWorkflowReadinessFollowUpCandidatesMax})
	if err != nil {
		return nil, mapErr(err)
	}
	out := []api.OperationWorkflowReadinessFollowUp{}
	for _, raw := range rows {
		var row api.OperationWorkflowReadinessFollowUp
		if err = json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *PgStore) PublishDueWorkflowReadinessReminders(ctx context.Context) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rows, err := q.LockDueReadinessReminderNotifications(ctx, tx, sqlc.LockDueReadinessReminderNotificationsParams{Now: readinessPGTime(now), BatchLimit: api.OperationWorkflowReadinessReminderBatchMax})
	if err != nil {
		return 0, mapErr(err)
	}
	published := 0
	for _, row := range rows {
		var n api.OperationWorkflowReadinessNotification
		if err = json.Unmarshal(row.Payload, &n); err != nil {
			return 0, err
		}
		if !readinessReminderDue(n, now) {
			continue
		}
		n.Reminder = readinessDueReminder(n, now)
		payload, err := json.Marshal(n)
		if err != nil {
			return 0, err
		}
		if _, err = q.UpdateReadinessNotification(ctx, tx, sqlc.UpdateReadinessNotificationParams{SubscriptionID: row.SubscriptionID, Sequence: row.Sequence, Payload: payload}); err != nil {
			return 0, mapErr(err)
		}
		published++
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return published, nil
}

func syncReadinessOutcomeEscalationTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, subscriptionID pgtype.UUID, scope string, source api.OperationWorkflowReadinessNotification) error {
	ref := source.BusinessOutcomeWatch.Escalation
	payload, err := q.LockReadinessNotification(ctx, tx, sqlc.LockReadinessNotificationParams{SubscriptionID: subscriptionID, Sequence: ref.Sequence})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	var n api.OperationWorkflowReadinessNotification
	if err = json.Unmarshal(payload, &n); err != nil {
		return err
	}
	updated, ok := syncReadinessOutcomeEscalation(n, source)
	if !ok {
		return nil
	}
	updated = captureObligationReceipts(ctx, uuid.UUID(subscriptionID.Bytes).String(), scope, n, updated, "")
	payload, err = json.Marshal(updated)
	if err != nil {
		return err
	}
	_, err = q.UpdateReadinessNotification(ctx, tx, sqlc.UpdateReadinessNotificationParams{SubscriptionID: subscriptionID, Sequence: ref.Sequence, Payload: payload})
	return mapErr(err)
}
