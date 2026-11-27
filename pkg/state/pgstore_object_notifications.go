package state

import (
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
	p, err := decodeObjectNotificationPolicy(b.ID, r.Revision, r.Rules)
	if err != nil {
		return p, err
	}
	return decodeManagedObjectNotifications(p, r.ManagedRules)
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
func pgNotificationQueue(ctx context.Context, db sqlc.DBTX, t api.ObjectNotificationTarget, env notificationEnvironment) (QueueBinding, error) {
	if env.Gone {
		return QueueBinding{}, ErrNotFound
	}
	bindings, err := sqlc.New().ObjectNotificationTargetQueue(ctx, db, sqlc.ObjectNotificationTargetQueueParams{AccountID: mustPgUUID(t.AccountID), AppID: mustPgUUID(t.AppID), QueueName: t.QueueName,
		Scope: env.Scope, EnvironmentID: nullablePgUUID(env.ID)})
	if err != nil {
		return QueueBinding{}, mapErr(err)
	}
	if len(bindings) != 1 {
		return QueueBinding{}, ErrNotFound
	}
	return queueConsumerBindingFromSQL(bindings[0]), nil
}

func pgObjectNotificationPolicy(ctx context.Context, db sqlc.DBTX, account, app string) (Account, error) {
	owner, err := queuePolicyAccountDB(ctx, db, account, app, true)
	if err != nil || owner.ResourceOwnershipEnabled {
		return owner, err
	}
	// Personal and not-yet-adopted resources retain the legacy account lock.
	row, err := sqlc.New().QueueConsumerLockAccount(ctx, db, mustPgUUID(account))
	if err != nil {
		return Account{}, mapErr(err)
	}
	owner.Plan = api.Plan(row.Plan)
	return owner, nil
}
func (s *PgStore) SetObjectBucketNotifications(ctx context.Context, account, app, bucket string, rules []api.ObjectNotificationRule) (api.ObjectBucketNotifications, error) {
	return s.changeObjectBucketNotifications(ctx, account, app, bucket, rules, nil)
}
func (s *PgStore) ReconcileObjectBucketComputeTriggers(ctx context.Context, account, app, bucket string, request api.ReconcileObjectStorageComputeTriggersRequest) (api.ObjectBucketNotifications, error) {
	return s.changeObjectBucketNotifications(ctx, account, app, bucket, nil, &request)
}
func (s *PgStore) changeObjectBucketNotifications(ctx context.Context, account, app, bucket string, rules []api.ObjectNotificationRule, request *api.ReconcileObjectStorageComputeTriggersRequest) (api.ObjectBucketNotifications, error) {
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
	owner, err := pgObjectNotificationPolicy(ctx, tx, account, app)
	if err != nil {
		return api.ObjectBucketNotifications{}, mapErr(err)
	}
	raw, err := q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return api.ObjectBucketNotifications{}, mapErr(err)
	}
	b := objectBucketFromSQL(raw)
	p, err := readObjectNotifications(ctx, tx, b)
	if err != nil {
		return p, err
	}
	next, err := objectNotificationChange(b, p, normal, request)
	if err != nil {
		return p, err
	}
	validateRules := next.Rules
	if request != nil {
		validateRules = managedObjectNotificationRules(next)
	}
	env, err := pgObjectBucketNotificationEnvironment(ctx, tx, b.ID)
	if err != nil {
		return p, err
	}
	if env.Gone && len(validateRules) > 0 {
		return p, ErrObjectNotificationInvalid
	}
	for _, r := range validateRules {
		t, e := notificationTarget(r, b)
		if e != nil || !notificationEntitled(owner.Plan, t.Kind) {
			return p, ErrObjectNotificationInvalid
		}
		if _, e = q.ObjectNotificationTargetApp(ctx, tx, sqlc.ObjectNotificationTargetAppParams{ID: mustPgUUID(t.AppID), AccountID: mustPgUUID(account)}); e != nil {
			return p, mapNotificationDestinationError(e)
		}
		if t.Kind == "function" && env.ID != "" {
			if _, e = q.ObjectNotificationLockQueueEnvironment(ctx, tx, sqlc.ObjectNotificationLockQueueEnvironmentParams{EnvironmentID: mustPgUUID(env.ID), DeploymentScope: env.Scope, AppID: mustPgUUID(t.AppID), AccountID: mustPgUUID(account)}); e != nil {
				return p, mapNotificationDestinationError(e)
			}
		}
		if producer := objectNotificationProducer(b); producer != "" {
			inReach, e := q.ObjectNotificationTargetInReach(ctx, tx, sqlc.ObjectNotificationTargetInReachParams{AccountID: mustPgUUID(account), Producer: producer, TargetAppID: t.AppID})
			if e != nil {
				return p, mapErr(e)
			}
			if !inReach {
				return p, ErrObjectNotificationInvalid
			}
		}
		if t.Kind == "queue" {
			if _, e = pgNotificationQueue(ctx, tx, t, env); e != nil {
				return p, mapNotificationDestinationError(e)
			}
		}
	}
	if sameObjectNotificationPolicy(p, next) {
		// An explicit write of identical rules still clears a clone's pause.
		if err := q.ObjectNotificationsClearClonePause(ctx, tx, mustPgUUID(bucket)); err != nil {
			return p, mapErr(err)
		}
		return p, tx.Commit(ctx)
	}
	if p.Revision >= api.MaxObjectStoragePolicyValue {
		return p, ErrConflict
	}
	next.Revision = p.Revision + 1
	p = next
	z, _ := json.Marshal(p.Rules)
	managed, _ := json.Marshal(managedObjectNotificationRules(p))
	err = q.ObjectNotificationsSave(ctx, tx, sqlc.ObjectNotificationsSaveParams{BucketID: mustPgUUID(bucket), Revision: p.Revision, Rules: z, ManagedRules: managed})
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

func captureObjectNotifications(ctx context.Context, db sqlc.DBTX, account, identity, typ string, d api.ObjectStorageEvent, payload []byte) error {
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
	if r.ClonePaused {
		return nil
	}
	p, err := decodeObjectNotificationPolicy(d.BucketID, r.Revision, r.Rules)
	if err != nil {
		return err
	}
	b := ObjectBucket{ID: d.BucketID, AccountID: account, AppID: pgUUIDString(r.AppID), Name: r.Name, Region: r.Region}
	env, err := pgObjectBucketNotificationEnvironment(ctx, db, d.BucketID)
	if err != nil {
		return err
	}
	recipients, err := notificationRecipients(b, env, p, typ, d, func(t api.ObjectNotificationTarget, env notificationEnvironment) (QueueBinding, error) {
		return pgNotificationQueue(ctx, db, t, env)
	})
	if err != nil || len(recipients) == 0 {
		return err
	}
	encoded, err := json.Marshal(recipients)
	if err != nil {
		return err
	}
	rows, err := sqlc.New().ObjectNotificationsCapture(ctx, db, sqlc.ObjectNotificationsCaptureParams{AccountID: mustPgUUID(account), EventID: identity, Recipients: encoded, Payload: payload})
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
	owner, err := pgObjectNotificationPolicy(ctx, tx, in.AccountID, in.AppID)
	if err != nil {
		return err
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
	if !notificationEntitled(owner.Plan, t.Kind) {
		return ErrObjectNotificationCapacity
	}
	// A function target of a stage bucket runs as the stage's work while the
	// stage exists (ADR-20261104120000 amendment 2026-11-16).
	if t.Kind == "function" && snapshot.EnvironmentID != "" {
		if _, e := q.ObjectNotificationLockQueueEnvironment(ctx, tx, sqlc.ObjectNotificationLockQueueEnvironmentParams{EnvironmentID: mustPgUUID(snapshot.EnvironmentID), DeploymentScope: snapshot.DeploymentScope, AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID)}); e != nil {
			return mapErr(e)
		}
		in.DeploymentScope = snapshot.DeploymentScope
	}
	if t.Kind == "queue" {
		binding, e := pgCapturedNotificationQueue(ctx, tx, t, snapshot.QueueBindingID)
		if e != nil {
			return e
		}
		// Environment admission precedes the app lock, matching queue mutations.
		if binding.EnvironmentID != "" {
			if _, e = q.ObjectNotificationLockQueueEnvironment(ctx, tx, sqlc.ObjectNotificationLockQueueEnvironmentParams{EnvironmentID: mustPgUUID(binding.EnvironmentID), DeploymentScope: binding.DeploymentScope, AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID)}); e != nil {
				return mapErr(e)
			}
		}
	}
	// FOR UPDATE also conflicts with the app FK's key-share locks held by other
	// invocation inserts, so counting and this admission share one critical section.
	if _, err = q.ObjectNotificationLockApp(ctx, tx, sqlc.ObjectNotificationLockAppParams{ID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID)}); err != nil {
		return mapErr(err)
	}
	if t.Kind == "queue" {
		row, e := q.QueueConsumerBindingForUpdate(ctx, tx, sqlc.QueueConsumerBindingForUpdateParams{ID: mustPgUUID(snapshot.QueueBindingID), AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID)})
		if e != nil {
			return mapErr(e)
		}
		binding := queueConsumerBindingFromSQL(row)
		if !notificationCapturedQueue(binding, t) {
			return ErrNotFound
		}
		in.QueueBindingID, in.DeploymentScope = binding.ID, binding.DeploymentScope
		n, e := q.ObjectNotificationQueueDepth(ctx, tx, mustPgUUID(in.AppID))
		if e != nil {
			return e
		}
		if n >= int64(api.MustLimitsFor(owner.Plan).MaxQueueDepth) {
			return ErrObjectNotificationCapacity
		}
	}
	err = q.ObjectNotificationInvocationInsert(ctx, tx, sqlc.ObjectNotificationInvocationInsertParams{ID: mustPgUUID(in.ID), AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID), Source: string(in.Source), QueueName: in.QueueName, Payload: in.Payload, Headers: in.Headers, DueAt: objectUsageTime(in.DueAt), RetryPolicy: snapshot.RetryPolicy, DeploymentScope: in.DeploymentScope, QueueBindingID: mustPgUUID(in.QueueBindingID)})
	if err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func pgCapturedNotificationQueue(ctx context.Context, db sqlc.DBTX, t api.ObjectNotificationTarget, id string) (QueueBinding, error) {
	row, err := sqlc.New().QueueBindingHistoryByID(ctx, db, sqlc.QueueBindingHistoryByIDParams{ID: mustPgUUID(id), AccountID: mustPgUUID(t.AccountID), AppID: mustPgUUID(t.AppID)})
	if err != nil {
		return QueueBinding{}, mapErr(err)
	}
	binding := queueConsumerBindingFromSQL(row)
	if !notificationCapturedQueue(binding, t) {
		return QueueBinding{}, ErrNotFound
	}
	return binding, nil
}

// pgObjectBucketNotificationEnvironment is the bucket's environment for its
// notification rules: production, a stage, or a stage that is gone.
func pgObjectBucketNotificationEnvironment(ctx context.Context, db sqlc.DBTX, bucketID string) (notificationEnvironment, error) {
	q := sqlc.New()
	id, err := q.ObjectBucketEventEnvironment(ctx, db, mustPgUUID(bucketID))
	if errors.Is(mapErr(err), ErrNotFound) || err == nil && id == "" {
		return notificationEnvironment{}, nil
	}
	if err != nil {
		return notificationEnvironment{}, mapErr(err)
	}
	slug, err := q.ObjectNotificationEnvironmentSlug(ctx, db, mustPgUUID(id))
	if errors.Is(mapErr(err), ErrNotFound) {
		return notificationEnvironment{Gone: true}, nil
	}
	if err != nil {
		return notificationEnvironment{}, mapErr(err)
	}
	return notificationEnvironment{Scope: slug, ID: id}, nil
}
