package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func enableRegistryFixture(t *testing.T) (*Quotas, durableentity.ID, *quotaBucket) {
	t.Helper()
	q, id, b, _ := retirementQuotaFixture(t)
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.EnableRegistry(t.Context(), id, uuid.NewString(), status.Revision, "operator"); err != nil {
		t.Fatal(err)
	}
	return q, id, b
}
func TestQuotaRegistryMigrationPreservesRetirementAndScalesPastLegacyCapacity(t *testing.T) {
	q, id, b, reader := retirementQuotaFixture(t)
	op := reader.status.Request.OperationID
	if _, err := q.TrackRetirement(t.Context(), id, op, reader); err != nil {
		t.Fatal(err)
	}
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.EnableRegistry(t.Context(), id, uuid.NewString(), status.Revision, "operator"); !errors.Is(err, durableentity.ErrInventoryPending) {
		t.Fatal("pending retirement migrated", err)
	}
	reader.status.Phase = "complete"
	reader.status.RetainedBytes = 128
	if _, err = q.ReclaimRetirement(t.Context(), id, op, reader); err != nil {
		t.Fatal(err)
	}
	status, err = q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	migration := uuid.NewString()
	if err = q.EnableRegistry(t.Context(), id, migration, status.Revision, "operator"); err != nil {
		t.Fatal(err)
	}
	if err = q.EnableRegistry(t.Context(), id, migration, status.Revision, "different-operator"); err != nil {
		t.Fatal("exact replay", err)
	}
	for i := 0; i < 70; i++ {
		fresh := id
		fresh.Key = fmt.Sprintf("document-%d", i)
		if err = q.AdmitInvocation(t.Context(), fresh); err != nil {
			t.Fatal(i, err)
		}
		now := q.now().Add(time.Minute)
		q.Now = func() time.Time { return now }
	}
	if err = q.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("migrated fence lost", err)
	}
	replay, err := q.ReclaimRetirement(t.Context(), id, op, reader)
	if err != nil || replay.ReleasedBytes != 896 {
		t.Fatal(replay, err)
	}
	first, err := q.ListRegistry(t.Context(), id, "")
	if err != nil || len(first.Entries) != 64 || first.NextCursor == "" || first.Active != 70 || first.Retired != 1 {
		t.Fatal(first, err)
	}
	second, err := q.ListRegistry(t.Context(), id, first.NextCursor)
	if err != nil || len(second.Entries) != 7 || second.NextCursor != "" {
		t.Fatal(second, err)
	}
	other := id
	other.TenantID = "other"
	if err = q.AdmitInvocation(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ListRegistry(t.Context(), other, first.NextCursor); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("cross-scope cursor", err)
	}
	fresh := id
	fresh.Key = "after-page"
	if err = q.AdmitInvocation(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ListRegistry(t.Context(), id, first.NextCursor); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("stale cursor", err)
	}
	scope, _ := scopeFor(id)
	body, _, err := b.Get(t.Context(), "tenant-quotas/v1/"+scopeKey(scope), maxQuotaRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	var root quotaRecord
	if json.Unmarshal(body, &root) != nil || root.Schema != 4 || len(root.Entities) != 0 || len(root.Retirements) != 0 || root.Registry == nil {
		t.Fatal("registry remained inline", string(body))
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.RecoveryBytes != 128 || usage.ReservedBytes != 71*1024+128 {
		t.Fatal(usage, err)
	}
}
func TestQuotaRegistryShardFailureDoesNotPublishAuthority(t *testing.T) {
	q, id, b, _ := retirementQuotaFixture(t)
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	op := uuid.NewString()
	b.uncertain = true
	if err = q.EnableRegistry(t.Context(), id, op, status.Revision, "operator"); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(err)
	}
	current, err := q.RegistryStatus(t.Context(), id)
	if err != nil || current.Enabled || current.Revision != status.Revision {
		t.Fatal(current, err)
	}
	if err = q.EnableRegistry(t.Context(), id, op, status.Revision, "operator"); err != nil {
		t.Fatal("retry of unpublished shards", err)
	}
	r, _, err := q.read(t.Context(), scopeKey(quotaScope{id.AccountID, id.AppID, id.EnvironmentID, id.TenantID}))
	if err != nil {
		t.Fatal(err)
	}
	path := registryPath(scopeKey(r.Scope), 0, r.Registry.Shards[0])
	b.mu.Lock()
	object := b.objects[path]
	object.body = []byte("{}")
	b.objects[path] = object
	b.mu.Unlock()
	if err = q.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrCorrupt) {
		t.Fatal("corrupt shard admitted", err)
	}
}

type registryLostRootStore struct {
	durableentity.ObjectStore
	lose bool
}

func (s *registryLostRootStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	v, err := s.ObjectStore.Put(ctx, key, body, version)
	if err == nil && s.lose && strings.HasPrefix(key, "tenant-quotas/v1/") {
		s.lose = false
		return "", errors.New("lost root acknowledgement")
	}
	return v, err
}
func TestQuotaRegistryLostRootAcknowledgementAndStaleMigration(t *testing.T) {
	q, id, b, _ := retirementQuotaFixture(t)
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	old := status.Revision
	if err = q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err = q.EnableRegistry(t.Context(), id, uuid.NewString(), old, "operator"); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("stale migration accepted", err)
	}
	status, err = q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	q.Store = &registryLostRootStore{ObjectStore: b, lose: true}
	op := uuid.NewString()
	if err = q.EnableRegistry(t.Context(), id, op, status.Revision, "operator"); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(err)
	}
	current, err := q.RegistryStatus(t.Context(), id)
	if err != nil || !current.Enabled || current.OperationID != op {
		t.Fatal(current, err)
	}
	if err = q.EnableRegistry(t.Context(), id, op, status.Revision, "operator"); err != nil {
		t.Fatal(err)
	}
	if err = q.EnableRegistry(t.Context(), id, op, old, "operator"); !errors.Is(err, durableentity.ErrRequestConflict) {
		t.Fatal("changed request replayed", err)
	}
}
func TestQuotaRegistryShardCapacityAndTenantBudgetStayBounded(t *testing.T) {
	q, id, _ := enableRegistryFixture(t)
	scope, _ := scopeFor(id)
	err := q.mutate(t.Context(), scopeKey(scope), &scope, func(r *quotaRecord, _ time.Time) error {
		r.Entities = nil
		for i := 0; i < 64; i++ {
			r.Entities = append(r.Entities, entityReservation{Key: fmt.Sprintf("0%063x", i), Bytes: q.Limits.EntityBytes})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	err = q.mutate(t.Context(), scopeKey(scope), &scope, func(r *quotaRecord, _ time.Time) error {
		r.Entities = append(r.Entities, entityReservation{Key: fmt.Sprintf("0%063x", 64), Bytes: q.Limits.EntityBytes})
		return nil
	})
	if !errors.Is(err, durableentity.ErrLimit) {
		t.Fatal("overflow published", err)
	}
	current, err := q.RegistryStatus(t.Context(), id)
	if err != nil || current.Revision != status.Revision {
		t.Fatal(current, err)
	}
	// The increased registry count cannot bypass the scope's existing byte budget.
	err = q.mutate(t.Context(), scopeKey(scope), &scope, func(r *quotaRecord, _ time.Time) error {
		r.Entities = []entityReservation{{Key: entityKey(id), Bytes: r.Limits.RetainedBytes}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := id
	fresh.Key = "over-budget"
	assertQuota(t, q.AdmitInvocation(t.Context(), fresh), "retained_bytes")
}

type registryInterleavingStore struct {
	durableentity.ObjectStore
	beforeRoot func()
}

func (s *registryInterleavingStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	if s.beforeRoot != nil && strings.HasPrefix(key, "tenant-quotas/v1/") {
		run := s.beforeRoot
		s.beforeRoot = nil
		run()
	}
	return s.ObjectStore.Put(ctx, key, body, version)
}
func TestQuotaRegistryAdmissionRechecksFenceAfterRootConflict(t *testing.T) {
	q, id, b := enableRegistryFixture(t)
	other := *q
	reader := retirementReader{status: &durableentity.RetirementStatus{Request: durableentity.RetirementRequest{OperationID: uuid.NewString()}, Actor: "operator", At: q.now(), Revision: 1, Phase: "complete", RetainedBytes: 128}}
	q.Store = &registryInterleavingStore{ObjectStore: b, beforeRoot: func() {
		if _, err := other.TrackRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
			t.Fatal(err)
		}
		if _, err := other.ReclaimRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
			t.Fatal(err)
		}
	}}
	if err := q.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("stale admission overwrote fence", err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 0 || usage.ReservedBytes != 128 {
		t.Fatal(usage, err)
	}
}

func TestQuotaRegistryMoreThan256PermanentReceiptsSurvivePolicyUpdate(t *testing.T) {
	q, id, _ := enableRegistryFixture(t)
	scope, _ := scopeFor(id)
	retiredID := id
	if err := q.mutate(t.Context(), scopeKey(scope), &scope, func(r *quotaRecord, _ time.Time) error {
		for i := 0; i < 257; i++ {
			entity := id
			entity.Key = fmt.Sprintf("archive-%d", i)
			if i == 0 {
				retiredID = entity
			}
			r.Retirements = append(r.Retirements, QuotaRetirement{EntityKey: entityKey(entity), OperationID: uuid.NewString(), Actor: "operator", At: q.now(), Phase: "complete", RetainedBytes: 8, ReleasedBytes: 1016})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.RetiredEntities != 257 || usage.RecoveryBytes != 257*8 {
		t.Fatal(usage, err)
	}
	policy, err := q.PolicyStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := q.MigrationStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	migration, err = q.StartMigration(t.Context(), id, uuid.NewString(), migration.ID, migration.Revision)
	if err != nil {
		t.Fatal(err)
	}
	q.Limits.EntityBytes *= 2
	p := QuotaPolicyRequest{OperationID: uuid.NewString(), ExpectedRevision: policy.Revision, ExpectedMigrationID: migration.ID, ExpectedMigrationRevision: migration.Revision, MigrationID: uuid.NewString(), Limits: q.Limits}
	if _, err = q.ApplyPolicy(t.Context(), id, p, "operator"); err != nil {
		t.Fatal(err)
	}
	now := q.now().Add(10 * time.Minute)
	q.Now = func() time.Time { return now }
	migration, err = q.MigrationStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	scan := &policyScanner{migrationScan: func(context.Context, string, durableentity.ID) (durableentity.QuotaInventoryPage, error) {
		e := migrationEntity(id, 10)
		e.StorageLimitBytes = q.Limits.EntityBytes
		return durableentity.QuotaInventoryPage{Entities: []durableentity.QuotaInventoryEntity{e}}, nil
	}}
	for migration.Phase != "complete" {
		migration, err = q.StepMigration(t.Context(), id, migration.ID, migration.Revision, scan)
		if err != nil {
			t.Fatal(err)
		}
	}
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil || !status.Enabled || status.Retired != 257 {
		t.Fatal(status, err)
	}
	if err = q.AdmitInvocation(t.Context(), retiredID); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("policy dropped permanent fence", err)
	}
	usage, err = q.Inspect(t.Context(), id)
	if err != nil || usage.RecoveryBytes != 257*8 || usage.ReservedBytes != 2048+257*8 {
		t.Fatal(usage, err)
	}
}
func TestQuotaRegistryUnknownCreditIsReplaySafe(t *testing.T) {
	q, id, b := enableRegistryFixture(t)
	reader := retirementReader{status: &durableentity.RetirementStatus{Request: durableentity.RetirementRequest{OperationID: uuid.NewString()}, Actor: "operator", At: q.now(), Revision: 1, Phase: "complete", RetainedBytes: 128}}
	if _, err := q.TrackRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
		t.Fatal(err)
	}
	q.Store = &registryLostRootStore{ObjectStore: b, lose: true}
	if _, err := q.ReclaimRetirement(t.Context(), id, reader.status.Request.OperationID, reader); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(err)
	}
	receipt, err := q.ReclaimRetirement(t.Context(), id, reader.status.Request.OperationID, reader)
	if err != nil || receipt.ReleasedBytes != 896 {
		t.Fatal(receipt, err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.ReservedBytes != 128 || usage.RecoveryBytes != 128 || usage.EntityReservations != 0 {
		t.Fatal(usage, err)
	}
}
