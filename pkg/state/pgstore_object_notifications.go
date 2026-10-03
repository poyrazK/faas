package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectNotificationStore = (*PgStore)(nil)

func readObjectNotifications(ctx context.Context, db sqlc.DBTX, b ObjectBucket) (api.ObjectBucketNotifications, error) {
	r, err := sqlc.New().ObjectNotificationsGet(ctx, db, mustPgUUID(b.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return api.ObjectBucketNotifications{BucketID: b.ID, Rules: []api.ObjectNotificationRule{}}, nil
	}
	if err != nil {
		return api.ObjectBucketNotifications{}, mapErr(err)
	}
	return decodeObjectNotificationPolicy(b.ID, r.Revision, r.Rules)
}
func (s *PgStore) GetObjectBucketNotifications(ctx context.Context, account, app, bucket string) (api.ObjectBucketNotifications, error) {
	b, err := s.GetObjectBucket(ctx, account, app, bucket)
	if err != nil {
		return api.ObjectBucketNotifications{}, err
	}
	if b.State != "ready" {
		return api.ObjectBucketNotifications{}, ErrConflict
	}
	return readObjectNotifications(ctx, s.pool, b)
}
func pgNotificationQueue(ctx context.Context, db sqlc.DBTX, t api.ObjectNotificationTarget) (string, []byte, error) {
	b, err := sqlc.New().ObjectNotificationTargetQueue(ctx, db, sqlc.ObjectNotificationTargetQueueParams{AccountID: mustPgUUID(t.AccountID), AppID: mustPgUUID(t.AppID), QueueName: t.QueueName})
	if err != nil {
		return "", nil, mapErr(err)
	}
	return pgUUIDString(b.ID), b.RetryPolicy, nil
}
func (s *PgStore) SetObjectBucketNotifications(ctx context.Context, account, app, bucket string, rules []api.ObjectNotificationRule) (api.ObjectBucketNotifications, error) {
	normal, err := api.NormalizeObjectNotificationRules(rules)
	if err != nil {
		return api.ObjectBucketNotifications{}, ErrObjectNotificationInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ObjectBucketNotifications{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	raw, err := q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return api.ObjectBucketNotifications{}, mapErr(err)
	}
	b := objectBucketFromSQL(raw)
	plan, err := q.ObjectNotificationLockAccount(ctx, tx, mustPgUUID(account))
	if err != nil {
		return api.ObjectBucketNotifications{}, mapErr(err)
	}
	p, err := readObjectNotifications(ctx, tx, b)
	if err != nil {
		return p, err
	}
	for _, r := range normal {
		t, e := notificationTarget(r, b)
		if e != nil || !notificationEntitled(api.Plan(plan), t.Kind) {
			return p, ErrObjectNotificationInvalid
		}
		if _, e = q.ObjectNotificationTargetApp(ctx, tx, sqlc.ObjectNotificationTargetAppParams{ID: mustPgUUID(t.AppID), AccountID: mustPgUUID(account)}); e != nil {
			return p, mapNotificationDestinationError(e)
		}
		if t.Kind == "queue" {
			if _, _, e = pgNotificationQueue(ctx, tx, t); e != nil {
				return p, mapNotificationDestinationError(e)
			}
		}
	}
	a, _ := json.Marshal(p.Rules)
	z, _ := json.Marshal(normal)
	if bytes.Equal(a, z) {
		return p, tx.Commit(ctx)
	}
	if p.Revision >= api.MaxObjectStoragePolicyValue {
		return p, ErrConflict
	}
	p.Revision++
	p.Rules = normal
	err = q.ObjectNotificationsSave(ctx, tx, sqlc.ObjectNotificationsSaveParams{BucketID: mustPgUUID(bucket), Revision: p.Revision, Rules: z})
	if err != nil {
		return p, mapErr(err)
	}
	return p, tx.Commit(ctx)
}
func mapNotificationDestinationError(err error) error {
	if errors.Is(mapErr(err), ErrNotFound) {
		return ErrObjectNotificationInvalid
	}
	return mapErr(err)
}

func captureObjectNotifications(ctx context.Context, db sqlc.DBTX, account, identity, typ string, d api.ObjectStorageEvent) error {
	r, err := sqlc.New().ObjectNotificationsGet(ctx, db, mustPgUUID(d.BucketID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if pgUUIDString(r.AccountID) != account {
		return ErrObjectNotificationInvalid
	}
	p, err := decodeObjectNotificationPolicy(d.BucketID, r.Revision, r.Rules)
	if err != nil {
		return err
	}
	b := ObjectBucket{ID: d.BucketID, AccountID: account, AppID: pgUUIDString(r.AppID), Name: r.Name, Region: r.Region}
	recipients, err := notificationRecipients(b, p, typ, d, func(t api.ObjectNotificationTarget) (string, []byte, error) { return pgNotificationQueue(ctx, db, t) })
	if err != nil || len(recipients) == 0 {
		return err
	}
	encoded, err := json.Marshal(recipients)
	if err != nil {
		return err
	}
	rows, err := sqlc.New().ObjectNotificationsCapture(ctx, db, sqlc.ObjectNotificationsCaptureParams{AccountID: mustPgUUID(account), EventID: identity, Recipients: encoded})
	if err != nil {
		return mapErr(err)
	}
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) EnqueueObjectNotification(ctx context.Context, in Invocation, snapshot ObjectNotificationSnapshot) error {
	t, err := validateNotificationInvocation(in, snapshot)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	plan, err := q.ObjectNotificationLockAccount(ctx, tx, mustPgUUID(in.AccountID))
	if err != nil {
		return mapErr(err)
	}
	// Check the exact previously committed admission before capacity or destination
	// checks: a lost checkpoint must not require another queue slot.
	old, err := q.ObjectNotificationInvocationExisting(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		if notificationSameIdentity(pgUUIDString(old.AppID), in.AppID) && notificationSameIdentity(pgUUIDString(old.AccountID), in.AccountID) && old.Source == string(in.Source) && old.QueueName == in.QueueName && jsonEqual(old.Payload, in.Payload) {
			return tx.Commit(ctx)
		}
		return ErrConflict
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return mapErr(err)
	}
	if !notificationEntitled(api.Plan(plan), t.Kind) {
		return ErrObjectNotificationCapacity
	}
	// FOR UPDATE also conflicts with the app FK's key-share locks held by other
	// invocation inserts, so counting and this admission share one critical section.
	if _, err = q.ObjectNotificationLockApp(ctx, tx, sqlc.ObjectNotificationLockAppParams{ID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID)}); err != nil {
		return mapErr(err)
	}
	if t.Kind == "queue" {
		id, _, e := pgNotificationQueue(ctx, tx, t)
		if e != nil {
			return e
		}
		if id != snapshot.QueueBindingID {
			return ErrNotFound
		}
		n, e := q.ObjectNotificationQueueDepth(ctx, tx, mustPgUUID(in.AppID))
		if e != nil {
			return e
		}
		if n >= int64(api.MustLimitsFor(api.Plan(plan)).MaxQueueDepth) {
			return ErrObjectNotificationCapacity
		}
	}
	err = q.ObjectNotificationInvocationInsert(ctx, tx, sqlc.ObjectNotificationInvocationInsertParams{ID: mustPgUUID(in.ID), AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID), Source: string(in.Source), QueueName: in.QueueName, Payload: in.Payload, Headers: in.Headers, DueAt: objectUsageTime(in.DueAt), RetryPolicy: snapshot.RetryPolicy})
	if err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}
