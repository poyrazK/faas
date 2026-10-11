package socket

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type registryCleanupBucket struct {
	*quotaBucket
	deletes      []string
	loseDelete   bool
	page         *durableentity.CleanupObjects
	beforeDelete func(string)
}

func (b *registryCleanupBucket) ListEntityObjects(_ context.Context, prefix, cursor string, limit int32) (durableentity.CleanupObjects, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.page != nil {
		return *b.page, nil
	}
	keys := []string{}
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) && key > cursor {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := durableentity.CleanupObjects{Keys: keys}
	if len(keys) > int(limit) {
		out.Keys = keys[:limit]
		out.NextCursor = out.Keys[len(out.Keys)-1]
	}
	return out, nil
}
func (b *registryCleanupBucket) DeleteEntityObject(_ context.Context, key string) error {
	if b.beforeDelete != nil {
		run := b.beforeDelete
		b.beforeDelete = nil
		run(key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletes = append(b.deletes, key)
	delete(b.objects, key)
	if b.loseDelete {
		b.loseDelete = false
		return errors.New("lost delete acknowledgement")
	}
	return nil
}
func registryCleanupFixture(t *testing.T) (*Quotas, durableentity.ID, *registryCleanupBucket) {
	t.Helper()
	q, id, b := enableRegistryFixture(t)
	store := &registryCleanupBucket{quotaBucket: b}
	q.Store = store
	cache, err := NewRegistryCache(store, 8<<20, 256, 16)
	if err != nil {
		t.Fatal(err)
	}
	q.RegistryCache = cache
	return q, id, store
}
func startRegistryCleanup(t *testing.T, q *Quotas, id durableentity.ID) *RegistryCleanup {
	t.Helper()
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := q.StartRegistryCleanup(t.Context(), id, uuid.NewString(), status.Revision, "operator")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func finishRegistryCleanup(t *testing.T, q *Quotas, id durableentity.ID, c *RegistryCleanup) *RegistryCleanup {
	t.Helper()
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	for i := 0; c.Phase != "complete"; i++ {
		if i > 100 {
			t.Fatal("cleanup did not finish", c)
		}
		var err error
		c, err = q.StepRegistryCleanup(t.Context(), id, c.OperationID, c.Revision, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func putRegistryFixture(t *testing.T, q *Quotas, key string) {
	t.Helper()
	if _, err := q.Store.Put(t.Context(), key, []byte("orphan"), ""); err != nil {
		t.Fatal(err)
	}
}
func TestRegistryCleanupRotatesEpochPreservesFencesAndReclaimsOnlyOldMetadata(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	reader := retirementReader{status: &durableentity.RetirementStatus{Request: durableentity.RetirementRequest{OperationID: uuid.NewString()}, Actor: "operator", At: q.now(), Revision: 1, Phase: "complete", RetainedBytes: 128}}
	if _, err := q.TrackRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ReclaimRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
		t.Fatal(err)
	}
	fresh := id
	fresh.Key = "live-document"
	if err := q.AdmitInvocation(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
	scope, _ := scopeFor(id)
	key := scopeKey(scope)
	for i := 0; i < 100; i++ {
		putRegistryFixture(t, q, registryPath(key, i%16, registryDigest([]byte(fmt.Sprint(i)))))
	}
	unknown := "tenant-registry/v1/" + key + "/README"
	currentOrphan := registryPath(key, 0, registryDigest([]byte("current-orphan")), 1)
	future := registryPath(key, 0, registryDigest([]byte("future")), 2)
	foreign := registryPath(strings.Repeat("a", 64), 0, registryDigest([]byte("foreign")))
	for _, path := range []string{unknown, currentOrphan, future, foreign} {
		putRegistryFixture(t, q, path)
	}
	c := startRegistryCleanup(t, q, id)
	if c.Epoch != 1 {
		t.Fatal(c)
	}
	if err := q.AdmitInvocation(t.Context(), fresh); err != nil {
		t.Fatal("current-epoch admission blocked", err)
	}
	if _, err := q.StartMigration(t.Context(), id, uuid.NewString(), "", 0); !errors.Is(err, ErrRegistryCleanupPending) {
		t.Fatal("migration escaped hold", err)
	}
	if _, err := q.StepRegistryCleanup(t.Context(), id, c.OperationID, c.Revision, false); !errors.Is(err, durableentity.ErrBusy) {
		t.Fatal("reader grace bypassed", err)
	}
	if preview, err := q.PreviewRetirement(t.Context(), fresh); err != nil || !preview.Ready {
		t.Fatal("retirement blocked during cleanup", preview, err)
	}
	original := *c
	c = finishRegistryCleanup(t, q, id, c)
	if c.DeletedObjects < 100 {
		t.Fatal(c)
	}
	for _, path := range []string{unknown, currentOrphan, future, foreign} {
		if _, _, err := q.Store.Get(t.Context(), path, 1024); err != nil {
			t.Fatal("unsafe deletion", path, err)
		}
	}
	if err := q.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("permanent fence lost", err)
	}
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.ReservedBytes != 1152 || usage.RecoveryBytes != 128 || usage.EntityReservations != 1 {
		t.Fatal(usage, err)
	}
	replay, err := q.StartRegistryCleanup(t.Context(), id, c.OperationID, original.SourceRevision, "different-operator")
	if err != nil || replay.Actor != "operator" || replay.Revision != c.Revision {
		t.Fatal(replay, err)
	}
	c = startRegistryCleanup(t, q, id)
	if c.Epoch != 2 {
		t.Fatal(c)
	}
	c = finishRegistryCleanup(t, q, id, c)
	if _, _, err = q.Store.Get(t.Context(), currentOrphan, 1024); !errors.Is(err, durableentity.ErrNotFound) {
		t.Fatal("old epoch orphan retained", err)
	}
	q.RegistryCache = nil // A cold read verifies current storage, not warm copies.
	if _, err = q.Inspect(t.Context(), id); err != nil {
		t.Fatal("current registry deleted", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, path := range b.deletes {
		if strings.HasPrefix(path, registryEpochPrefix(key, 2)) {
			t.Fatal("current epoch deleted", path)
		}
	}
}
func TestRegistryCleanupUnknownRootAndDeleteAcknowledgementsResume(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	lost := &registryLostRootStore{ObjectStore: b, lose: true}
	q.Store = &registryCleanupLostRoot{registryLostRootStore: lost, CleanupStore: b}
	op := uuid.NewString()
	if _, err = q.StartRegistryCleanup(t.Context(), id, op, status.Revision, "operator"); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(err)
	}
	c, err := q.StartRegistryCleanup(t.Context(), id, op, status.Revision, "operator")
	if err != nil || c.Epoch != 1 {
		t.Fatal(c, err)
	}
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	revision := c.Revision
	b.loseDelete = true
	if _, err = q.StepRegistryCleanup(t.Context(), id, op, revision, false); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(err)
	}
	c, err = q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || c.Revision != revision {
		t.Fatal("delete failure advanced checkpoint", c, err)
	}
	c, err = q.StepRegistryCleanup(t.Context(), id, op, c.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.StepRegistryCleanup(t.Context(), id, op, revision, false); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("stale checkpoint accepted", err)
	}
	lost.lose = true
	if _, err = q.StepRegistryCleanup(t.Context(), id, op, c.Revision, false); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal(err)
	}
	current, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.Revision != c.Revision+1 {
		t.Fatal("lost checkpoint not inspectable", current, err)
	}
	finishRegistryCleanup(t, q, id, current)
}

type registryCleanupLostRoot struct {
	*registryLostRootStore
	durableentity.CleanupStore
}

func TestRegistryCleanupInvalidPageDeletesNothingAndProbeDeletesOnlyOwnKey(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	if err := q.CheckRegistryCleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(b.deletes) != 1 || !strings.HasPrefix(b.deletes[0], "tenant-registry/probes/") {
		t.Fatal(b.deletes)
	}
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	for _, page := range []durableentity.CleanupObjects{{Keys: []string{"outside"}}, {Keys: []string{"tenant-registry/v1/outside/"}, NextCursor: "next"}, {Keys: make([]string, api.DurableEntityCleanupPageSize+1)}, {NextCursor: "next"}} {
		b.page = &page
		before := len(b.deletes)
		if _, err := q.StepRegistryCleanup(t.Context(), id, c.OperationID, c.Revision, false); !errors.Is(err, durableentity.ErrCorrupt) {
			t.Fatal(err)
		}
		if len(b.deletes) != before {
			t.Fatal("deleted before validating whole page")
		}
	}
	b.page = nil
	finishRegistryCleanup(t, q, id, c)
}
func TestRegistryCleanupDelayedDeleteCannotReachReusedHashInNewEpoch(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	first := finishRegistryCleanup(t, q, id, startRegistryCleanup(t, q, id))
	second := startRegistryCleanup(t, q, id)
	now := second.NotBefore
	q.Now = func() time.Time { return now }
	for second.Phase != "epoch_cleanup" {
		var err error
		second, err = q.StepRegistryCleanup(t.Context(), id, second.OperationID, second.Revision, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	other := *q
	other.RegistryCache = nil
	b.beforeDelete = func(path string) {
		if !obsoleteRegistryKey(scopeKey(quotaScope{id.AccountID, id.AppID, id.EnvironmentID, id.TenantID}), path, second.Epoch) {
			t.Fatal(path)
		}
		finishRegistryCleanup(t, &other, id, second)
		if err := other.AdmitInvocation(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.StepRegistryCleanup(t.Context(), id, second.OperationID, second.Revision, false); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("delayed checkpoint should conflict", err)
	}
	status, err := other.RegistryStatus(t.Context(), id)
	if err != nil || status.Epoch != first.Epoch+1 {
		t.Fatal(status, err)
	}
	if _, err = other.Inspect(t.Context(), id); err != nil {
		t.Fatal("delayed worker deleted live reused hash", err)
	}
}

type registryCleanupInterleaving struct {
	*registryInterleavingStore
	durableentity.CleanupStore
}

func TestRegistryCleanupStaleCutoverDoesNotDeleteOrLoseReservations(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	status, err := q.RegistryStatus(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	other := *q
	other.Store = b
	q.Store = &registryCleanupInterleaving{registryInterleavingStore: &registryInterleavingStore{ObjectStore: b, beforeRoot: func() {
		fresh := id
		fresh.Key = "concurrent-new-document"
		if err := other.AdmitInvocation(t.Context(), fresh); err != nil {
			t.Fatal(err)
		}
	}}, CleanupStore: b}
	if _, err = q.StartRegistryCleanup(t.Context(), id, uuid.NewString(), status.Revision, "operator"); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("stale cutover accepted", err)
	}
	if len(b.deletes) != 0 {
		t.Fatal("deleted before root cutover")
	}
	state, err := other.RegistryStatus(t.Context(), id)
	if err != nil || state.Epoch != 0 || state.Active != 2 {
		t.Fatal(state, err)
	}
	finishRegistryCleanup(t, q, id, startRegistryCleanup(t, q, id))
	q.RegistryCache = nil
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.EntityReservations != 2 || usage.ReservedBytes != 2048 {
		t.Fatal("cutover lost concurrent reservation", usage, err)
	}
}

func TestRegistryCleanupCheckpointMergesConcurrentQuotaMutations(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	lease, err := q.AcquireConnection(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	c := startRegistryCleanup(t, q, id) // Existing connections need not drain.
	if err = q.ReleaseConnection(t.Context(), id, lease); err != nil {
		t.Fatal("release blocked during grace", err)
	}
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	other := *q
	other.RegistryCache = nil
	fresh := id
	fresh.Key = "new-during-collection"
	var granted Grant
	q.Store = &registryCleanupInterleaving{registryInterleavingStore: &registryInterleavingStore{ObjectStore: b, beforeRoot: func() {
		if err := other.AdmitInvocation(t.Context(), fresh); err != nil {
			t.Fatal(err)
		}
		scope, _ := scopeFor(id)
		g := testGrant(fresh)
		g.ID = scopeKey(scope) + "." + uuid.NewString()
		g.ExpiresAt = now.Add(time.Minute)
		granted, err = other.mint(t.Context(), g)
		if err != nil {
			t.Fatal(err)
		}
		reader := retirementReader{status: &durableentity.RetirementStatus{Request: durableentity.RetirementRequest{OperationID: uuid.NewString()}, Actor: "operator", At: now, Revision: 1, Phase: "complete", RetainedBytes: 128}}
		if _, err := other.TrackRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
			t.Fatal(err)
		}
		if _, err := other.ReclaimRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
			t.Fatal(err)
		}
		lease, err = other.AcquireConnection(t.Context(), fresh)
		if err != nil {
			t.Fatal(err)
		}
	}}, CleanupStore: b}
	c, err = q.StepRegistryCleanup(t.Context(), id, c.OperationID, c.Revision, false)
	if err != nil || c.Revision != 2 {
		t.Fatal("checkpoint did not merge admission conflict", c, err)
	}
	u, err := other.Inspect(t.Context(), fresh)
	if err != nil || u.EntityReservations != 1 || u.GrantBytes <= 0 || u.ReservedBytes != 1152+u.GrantBytes || u.RecoveryBytes != 128 || u.Invocations != 1 || u.Grants != 1 || u.GrantsIssued != 1 || u.Connections != 1 {
		t.Fatal("checkpoint lost concurrent quota state", u, err)
	}
	if err := other.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("checkpoint lost retirement fence", err)
	}
	if err := other.revoke(t.Context(), granted.ID, id.AccountID, id.AppID); err != nil {
		t.Fatal(err)
	}
	if err := other.ReleaseConnection(t.Context(), fresh, lease); err != nil {
		t.Fatal(err)
	}
	finishRegistryCleanup(t, q, id, c)
	if _, err := other.Inspect(t.Context(), fresh); err != nil {
		t.Fatal("cold reader lost live shards", err)
	}
}

func TestRegistryCleanupLegacySchemaFiveRetainsHold(t *testing.T) {
	q, id, _ := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	scope, _ := scopeFor(id)
	key := scopeKey(scope)
	r, version, err := q.read(t.Context(), key)
	if err != nil || r.Schema != 6 {
		t.Fatal(r.Schema, err)
	}
	r.Schema = 5 // A persisted cleanup created by the preceding release.
	if err := q.commitRegistryCleanup(t.Context(), key, r, version); err != nil {
		t.Fatal(err)
	}
	if err := q.AdmitInvocation(t.Context(), id); !errors.Is(err, ErrRegistryCleanupPending) {
		t.Fatal("legacy hold bypassed", err)
	}
	if preview, err := q.PreviewRetirement(t.Context(), id); err != nil || preview.Ready {
		t.Fatal(preview, err)
	}
	finishRegistryCleanup(t, q, id, c)
	if err := q.AdmitInvocation(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	startRegistryCleanup(t, q, id) // A later rotation upgrades to schema 6.
	r, _, err = q.read(t.Context(), key)
	if err != nil || r.Schema != 6 || registryCleanupHoldsWrites(r) {
		t.Fatal(r.Schema, err)
	}
}

type registryCleanupContentionStore struct {
	*registryCleanupBucket
	beforeRoot func()
	attempts   int
}

func (s *registryCleanupContentionStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	if strings.HasPrefix(key, "tenant-quotas/v1/") {
		s.attempts++
		s.beforeRoot()
	}
	return s.registryCleanupBucket.Put(ctx, key, body, version)
}

func TestRegistryCleanupContentionIsBoundedWithoutRepeatingDeletes(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	other := *q
	s := &registryCleanupContentionStore{registryCleanupBucket: b, beforeRoot: func() {
		// A definite concurrent root update on every checkpoint attempt.
		if err := other.ReleaseConnection(t.Context(), id, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}}
	q.Store = s
	if _, err := q.StepRegistryCleanup(t.Context(), id, c.OperationID, c.Revision, false); !errors.Is(err, durableentity.ErrBusy) || s.attempts != 8 {
		t.Fatal("unbounded or hidden contention", s.attempts, err)
	}
	seen := map[string]bool{}
	for _, path := range b.deletes {
		if seen[path] {
			t.Fatal("repeated page deletion during CAS retries", path)
		}
		seen[path] = true
	}
	if len(seen) != 16 {
		t.Fatal("page not collected", len(seen))
	}
	status, err := other.RegistryCleanupStatus(t.Context(), id)
	if err != nil || status.Revision != c.Revision {
		t.Fatal("busy checkpoint advanced", status, err)
	}
	q.Store = b
	finishRegistryCleanup(t, q, id, status)
}
