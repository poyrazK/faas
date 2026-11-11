package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectBucketAllVersionStartStore starts a provisioning bucket in
// all-version accounting (ADR-20261107214517). A provider that reports a
// never-versioned bucket as Suspended (GCS, ADR-628) is converted to
// all-version accounting by its first write or delete anyway
// (ADR-20261023223000); doing it while the bucket is still provisioning and
// provably empty records the same end state without fencing a customer
// request. It reports false and changes nothing when the bucket is not
// provisioning or already has any accounting, versioning or inventory row.
type ObjectBucketAllVersionStartStore interface {
	StartObjectBucketAllVersionAccounting(ctx context.Context, account, bucket string) (bool, error)
}

var (
	_ ObjectBucketAllVersionStartStore = (*PgStore)(nil)
	_ ObjectBucketAllVersionStartStore = (*MemStore)(nil)
)

func (s *PgStore) StartObjectBucketAllVersionAccounting(ctx context.Context, account, bucket string) (bool, error) {
	if _, err := uuid.Parse(account); err != nil {
		return false, ErrNotFound
	}
	if _, err := uuid.Parse(bucket); err != nil {
		return false, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectBucketLockProvisioning(ctx, tx, sqlc.ObjectBucketLockProvisioningParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account)}); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	startable, err := q.ObjectBucketAllVersionStartable(ctx, tx, sqlc.ObjectBucketAllVersionStartableParams{BucketID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
	if err != nil || !startable {
		return false, err
	}
	job := mustPgUUID(uuid.NewString())
	if err = q.ObjectBucketAllVersionStartInventory(ctx, tx, sqlc.ObjectBucketAllVersionStartInventoryParams{ID: job, BucketID: mustPgUUID(bucket)}); err != nil {
		return false, mapErr(err)
	}
	if err = q.ObjectBucketAllVersionStartVersioning(ctx, tx, sqlc.ObjectBucketAllVersionStartVersioningParams{BucketID: mustPgUUID(bucket), CapacityJobID: job}); err != nil {
		return false, mapErr(err)
	}
	if err = q.ObjectBucketAllVersionStartUsage(ctx, tx, mustPgUUID(bucket)); err != nil {
		return false, mapErr(err)
	}
	return true, tx.Commit(ctx)
}

func (m *MemStore) StartObjectBucketAllVersionAccounting(ctx context.Context, account, bucket string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.State != "provisioning" {
		return false, nil
	}
	if _, ok := m.objectUsage[bucket]; ok {
		return false, nil
	}
	if _, ok := m.objectBucketVersioning[bucket]; ok {
		return false, nil
	}
	if len(m.objectGrants[bucket]) != 0 || len(m.objectTrackedGrants[bucket]) != 0 {
		return false, nil
	}
	for _, j := range m.objectCapacityJobs {
		if j.BucketID == bucket {
			return false, nil
		}
	}
	for _, w := range m.objectWriteAdmissions {
		if w.BucketID == bucket {
			return false, nil
		}
	}
	now := m.clock()
	job := ObjectCapacityReconciliation{ObjectCapacityReconciliation: api.ObjectCapacityReconciliation{ID: uuid.NewString(), BucketID: bucket, State: "completed", InventoryScope: ObjectInventoryAllVersions, CreatedAt: now, UpdatedAt: now, FinishedAt: &now},
		AccountID: b.AccountID, AppID: b.AppID, RetryAt: now, DeadlineAt: now, InventoryVerified: true}
	if m.objectCapacityJobs == nil {
		m.objectCapacityJobs = map[string]ObjectCapacityReconciliation{}
	}
	m.objectCapacityJobs[job.ID] = job
	propagated := now
	m.saveObjectVersioningLocked(ObjectBucketVersioning{ObjectBucketVersioning: api.ObjectBucketVersioning{BucketID: bucket, DesiredStatus: "Suspended", ObservedStatus: "Suspended", State: "ready", Revision: 1, VersionsRequired: true, PropagationUntil: &propagated, CapacityJobID: job.ID, UpdatedAt: now},
		AccountID: b.AccountID, AppID: b.AppID, RetryAt: now})
	if m.objectUsage == nil {
		m.objectUsage = map[string]ObjectBucketUsage{}
	}
	m.objectUsage[bucket] = ObjectBucketUsage{Bucket: b, ObservedAt: now, AttemptAt: now, InventoryScope: ObjectInventoryAllVersions}
	return true, nil
}
