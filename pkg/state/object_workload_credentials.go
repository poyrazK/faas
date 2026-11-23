package state

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectWorkloadCredentialAccessKeyPrefix starts every workload credential's
// access key (ADR-20261122191423 slice 2). Customer and compute keys start
// GRGA, so a key names the table it lives in.
const ObjectWorkloadCredentialAccessKeyPrefix = "GRGW"

// ObjectWorkloadCredentialMaxTTL is the longest a workload credential lives.
const ObjectWorkloadCredentialMaxTTL = api.WorkloadObjectCredentialTTL

// ObjectWorkloadCredentialManagedPrefix marks a resolved workload credential
// in the ObjectS3Credential shape the gateway authorizes.
const ObjectWorkloadCredentialManagedPrefix = "WORKLOAD"

// ObjectWorkloadCredential is one instance's short-lived S3 credential for a
// bucket its app's gregale.yaml `uses:` grants. It is never listed with the
// account's credentials, never cloned and does not count against the
// per-bucket key cap.
type ObjectWorkloadCredential struct {
	ID           string
	AccountID    string
	BucketID     string
	CallerAppID  string
	InstanceID   string
	AccessKeyID  string
	SecretSealed []byte
	KID          string
	Permission   string
	KeyPrefix    string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// ObjectWorkloadCredentialStore issues and sweeps workload credentials.
// ResolveObjectS3Credential resolves their access keys.
type ObjectWorkloadCredentialStore interface {
	CreateObjectWorkloadCredential(context.Context, ObjectWorkloadCredential) error
	DeleteExpiredObjectWorkloadCredentials(ctx context.Context, before time.Time, limit int) (int64, error)
	// CountLiveObjectWorkloadCredentials counts one instance's unexpired
	// credentials for one bucket, so apid can cap how many it holds.
	CountLiveObjectWorkloadCredentials(ctx context.Context, instanceID, bucketID string) (int, error)
	// ListLiveObjectWorkloadCredentials returns callerAppID's unexpired
	// credentials (ID, BucketID, Permission and KeyPrefix only).
	ListLiveObjectWorkloadCredentials(ctx context.Context, callerAppID string) ([]ObjectWorkloadCredential, error)
	// DeleteObjectWorkloadCredentials revokes callerAppID's credentials ids.
	DeleteObjectWorkloadCredentials(ctx context.Context, callerAppID string, ids []string) (int64, error)
}

func validObjectWorkloadCredential(c ObjectWorkloadCredential) bool {
	return validUUID(c.ID) && validUUID(c.AccountID) && validUUID(c.BucketID) && validUUID(c.CallerAppID) &&
		c.InstanceID != "" && len(c.InstanceID) <= 128 &&
		strings.HasPrefix(c.AccessKeyID, ObjectWorkloadCredentialAccessKeyPrefix) && len(c.AccessKeyID) == 20 &&
		len(c.SecretSealed) > 0 && c.KID != "" && len(c.KID) <= 255 &&
		(c.Permission == ObjectBucketPermissionRead || c.Permission == ObjectBucketPermissionWrite || c.Permission == ObjectBucketPermissionReadWrite) &&
		len(c.KeyPrefix) <= 512 && !strings.HasPrefix(c.KeyPrefix, "/") &&
		!c.CreatedAt.IsZero() && c.ExpiresAt.After(c.CreatedAt) && !c.ExpiresAt.After(c.CreatedAt.Add(ObjectWorkloadCredentialMaxTTL+5*time.Minute))
}

// objectWorkloadAsS3Credential is the credential shape the gateway already
// authorizes: a compute-style key (ManagedAppID set, so never bucket
// settings) limited to its key prefix.
func objectWorkloadAsS3Credential(c ObjectWorkloadCredential, bucket ObjectBucket) ObjectS3Credential {
	return ObjectS3Credential{
		ID: c.ID, AccountID: c.AccountID, BucketID: c.BucketID, AccessKeyID: c.AccessKeyID,
		SecretSealed: append([]byte(nil), c.SecretSealed...), KID: c.KID, Label: "workload", Permission: c.Permission,
		Status: ObjectS3CredentialStatusActive, CreatedAt: c.CreatedAt,
		ManagedAppID: c.CallerAppID, ManagedScope: bucket.Scope, ManagedPrefix: ObjectWorkloadCredentialManagedPrefix,
		KeyPrefix: c.KeyPrefix,
	}
}

func isObjectWorkloadAccessKey(accessKeyID string) bool {
	return strings.HasPrefix(accessKeyID, ObjectWorkloadCredentialAccessKeyPrefix)
}

// PgStore

func (s *PgStore) CreateObjectWorkloadCredential(ctx context.Context, c ObjectWorkloadCredential) error {
	if !validObjectWorkloadCredential(c) {
		return ErrInvalidArgument
	}
	prefix := pgtype.Text{String: c.KeyPrefix, Valid: c.KeyPrefix != ""}
	return mapErr(sqlc.New().ObjectWorkloadCredentialInsert(ctx, s.pool, sqlc.ObjectWorkloadCredentialInsertParams{
		ID: mustPgUUID(c.ID), AccountID: mustPgUUID(c.AccountID), BucketID: mustPgUUID(c.BucketID), CallerAppID: mustPgUUID(c.CallerAppID),
		InstanceID: c.InstanceID, AccessKeyID: c.AccessKeyID, SecretSealed: c.SecretSealed, Kid: c.KID, Permission: c.Permission,
		KeyPrefix: prefix, CreatedAt: pgtype.Timestamptz{Time: c.CreatedAt.UTC(), Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: c.ExpiresAt.UTC(), Valid: true},
	}))
}

func (s *PgStore) DeleteExpiredObjectWorkloadCredentials(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 10_000 {
		limit = 1000
	}
	n, err := sqlc.New().ObjectWorkloadCredentialDeleteExpired(ctx, s.pool, sqlc.ObjectWorkloadCredentialDeleteExpiredParams{
		Before: pgtype.Timestamptz{Time: before.UTC(), Valid: true}, BatchLimit: int32(limit)})
	return n, mapErr(err)
}

func (s *PgStore) CountLiveObjectWorkloadCredentials(ctx context.Context, instanceID, bucketID string) (int, error) {
	if !validUUID(bucketID) {
		return 0, ErrInvalidArgument
	}
	n, err := sqlc.New().ObjectWorkloadCredentialCountLive(ctx, s.pool, sqlc.ObjectWorkloadCredentialCountLiveParams{InstanceID: instanceID, BucketID: mustPgUUID(bucketID)})
	return int(n), mapErr(err)
}

func (s *PgStore) ListLiveObjectWorkloadCredentials(ctx context.Context, callerAppID string) ([]ObjectWorkloadCredential, error) {
	if !validUUID(callerAppID) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ObjectWorkloadCredentialListLiveForApp(ctx, s.pool, mustPgUUID(callerAppID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectWorkloadCredential, 0, len(rows))
	for _, row := range rows {
		out = append(out, ObjectWorkloadCredential{ID: pgUUIDString(row.ID), BucketID: pgUUIDString(row.BucketID), CallerAppID: callerAppID,
			Permission: row.Permission, KeyPrefix: row.KeyPrefix.String})
	}
	return out, nil
}

func (s *PgStore) DeleteObjectWorkloadCredentials(ctx context.Context, callerAppID string, ids []string) (int64, error) {
	if !validUUID(callerAppID) {
		return 0, ErrInvalidArgument
	}
	if len(ids) == 0 {
		return 0, nil
	}
	pgIDs := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		if !validUUID(id) {
			return 0, ErrInvalidArgument
		}
		pgIDs = append(pgIDs, mustPgUUID(id))
	}
	n, err := sqlc.New().ObjectWorkloadCredentialDelete(ctx, s.pool, sqlc.ObjectWorkloadCredentialDeleteParams{CallerAppID: mustPgUUID(callerAppID), Ids: pgIDs})
	return n, mapErr(err)
}

func (s *PgStore) resolveObjectWorkloadCredential(ctx context.Context, accessKeyID string) (ObjectS3Credential, ObjectBucket, error) {
	row, err := sqlc.New().ObjectWorkloadCredentialResolve(ctx, s.pool, accessKeyID)
	if err != nil {
		return ObjectS3Credential{}, ObjectBucket{}, mapErr(err)
	}
	bucket := ObjectBucket{
		ID: pgUUIDString(row.BucketID), OrgID: pgUUIDString(row.OrgID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID),
		Name: row.BucketName, Scope: row.BucketScope, Region: row.BucketRegion,
		BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, PhysicalName: row.PhysicalName,
		State: row.BucketState, CreatedAt: row.BucketCreatedAt.Time, UpdatedAt: row.BucketUpdatedAt.Time,
	}
	c := ObjectWorkloadCredential{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), BucketID: pgUUIDString(row.BucketID), CallerAppID: pgUUIDString(row.CallerAppID),
		AccessKeyID: row.AccessKeyID, SecretSealed: row.SecretSealed, KID: row.Kid, Permission: row.Permission, KeyPrefix: row.KeyPrefix.String,
		CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time,
	}
	return objectWorkloadAsS3Credential(c, bucket), bucket, nil
}

// MemStore

func (m *MemStore) CreateObjectWorkloadCredential(ctx context.Context, c ObjectWorkloadCredential) error {
	if !validObjectWorkloadCredential(c) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objectWorkloadCredentials == nil {
		m.objectWorkloadCredentials = map[string]ObjectWorkloadCredential{}
	}
	for _, existing := range m.objectWorkloadCredentials {
		if existing.AccessKeyID == c.AccessKeyID || existing.ID == c.ID {
			return ErrConflict
		}
	}
	c.SecretSealed = append([]byte(nil), c.SecretSealed...)
	m.objectWorkloadCredentials[c.ID] = c
	return nil
}

func (m *MemStore) DeleteExpiredObjectWorkloadCredentials(_ context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 10_000 {
		limit = 1000
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, c := range m.objectWorkloadCredentials {
		if n >= int64(limit) {
			break
		}
		if !c.ExpiresAt.After(before) {
			delete(m.objectWorkloadCredentials, id)
			n++
		}
	}
	return n, nil
}

func (m *MemStore) CountLiveObjectWorkloadCredentials(_ context.Context, instanceID, bucketID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	n := 0
	for _, c := range m.objectWorkloadCredentials {
		if c.InstanceID == instanceID && c.BucketID == bucketID && c.ExpiresAt.After(now) {
			n++
		}
	}
	return n, nil
}

func (m *MemStore) ListLiveObjectWorkloadCredentials(_ context.Context, callerAppID string) ([]ObjectWorkloadCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var out []ObjectWorkloadCredential
	for _, c := range m.objectWorkloadCredentials {
		if c.CallerAppID == callerAppID && c.ExpiresAt.After(now) {
			out = append(out, ObjectWorkloadCredential{ID: c.ID, BucketID: c.BucketID, CallerAppID: c.CallerAppID, Permission: c.Permission, KeyPrefix: c.KeyPrefix})
		}
	}
	return out, nil
}

func (m *MemStore) DeleteObjectWorkloadCredentials(_ context.Context, callerAppID string, ids []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, id := range ids {
		if c, ok := m.objectWorkloadCredentials[id]; ok && c.CallerAppID == callerAppID {
			delete(m.objectWorkloadCredentials, id)
			n++
		}
	}
	return n, nil
}

// workloadCredentialHolderLiveLocked mirrors the resolve query's caller and
// instance checks: the caller app exists and the instance still holds a VM.
func (m *MemStore) workloadCredentialHolderLiveLocked(c ObjectWorkloadCredential) bool {
	app, ok := m.apps[c.CallerAppID]
	if !ok || app.Status == AppDeleted {
		return false
	}
	instance, ok := m.instances[c.InstanceID]
	if !ok || instance.AppID != c.CallerAppID {
		return false
	}
	switch State(instance.State) {
	case StateParked, StateStopped, StateFailed, StateEvictingAccountDeleting:
		return false
	}
	return true
}

// resolveObjectWorkloadCredentialLocked mirrors ObjectWorkloadCredentialResolve.
func (m *MemStore) resolveObjectWorkloadCredentialLocked(accessKeyID string) (ObjectS3Credential, ObjectBucket, error) {
	now := time.Now()
	for _, c := range m.objectWorkloadCredentials {
		if c.AccessKeyID != accessKeyID || !c.ExpiresAt.After(now) {
			continue
		}
		bucket, ok := m.objectBuckets[c.BucketID]
		if !ok || bucket.AccountID != c.AccountID || bucket.State != "ready" || !m.cloneBucketAccessibleLocked(bucket) ||
			!m.workloadCredentialHolderLiveLocked(c) ||
			!m.objectCredentialLiveLocked(ObjectS3Credential{ManagedAppID: c.CallerAppID, BucketID: c.BucketID, AccountID: c.AccountID, Status: ObjectS3CredentialStatusActive}, bucket) {
			return ObjectS3Credential{}, ObjectBucket{}, ErrNotFound
		}
		return objectWorkloadAsS3Credential(c, bucket), bucket, nil
	}
	return ObjectS3Credential{}, ObjectBucket{}, ErrNotFound
}

func (m *MemStore) isObjectWorkloadCredentialLocked(id string) bool {
	_, ok := m.objectWorkloadCredentials[id]
	return ok
}
