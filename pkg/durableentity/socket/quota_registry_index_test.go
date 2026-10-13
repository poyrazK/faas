package socket

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type registryIndexFaultStore struct {
	*registryCleanupBucket
	failPut   bool
	losePut   bool
	failList  bool
	rootReads map[string]int
	lists     []string
}

func (s *registryIndexFaultStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	if strings.HasPrefix(key, registryCleanupIndexPrefix) && s.failPut {
		return "", errors.New("index unavailable")
	}
	v, err := s.registryCleanupBucket.Put(ctx, key, body, version)
	if err == nil && strings.HasPrefix(key, registryCleanupIndexPrefix) && s.losePut {
		s.losePut = false
		return "", errors.New("lost index acknowledgement")
	}
	return v, err
}
func (s *registryIndexFaultStore) Get(ctx context.Context, key string, limit int64) ([]byte, string, error) {
	if s.rootReads != nil && strings.HasPrefix(key, "tenant-quotas/v1/") {
		s.rootReads[key]++
	}
	return s.registryCleanupBucket.Get(ctx, key, limit)
}
func (s *registryIndexFaultStore) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (durableentity.CleanupObjects, error) {
	s.lists = append(s.lists, prefix)
	if s.failList && prefix == registryCleanupIndexPrefix {
		return durableentity.CleanupObjects{}, errors.New("index listing unavailable")
	}
	return s.registryCleanupBucket.ListEntityObjects(ctx, prefix, cursor, limit)
}
func indexScopeKey(id durableentity.ID) string { scope, _ := scopeFor(id); return scopeKey(scope) }
func indexedSweep(t *testing.T, q *Quotas) RegistryCleanupWorkerPage {
	t.Helper()
	p, err := q.SweepIndexedRegistryCleanups(t.Context(), "", allowRegistryWorker)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRegistryCleanupIndexPublicationFailureDoesNotChangeStartAcknowledgement(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			q, id, b := registryCleanupFixture(t)
			fault := &registryIndexFaultStore{registryCleanupBucket: b, failPut: !lost, losePut: lost}
			q.Store = fault
			c := startRegistryCleanup(t, q, id)
			current, err := q.RegistryCleanupStatus(t.Context(), id)
			if err != nil || current.OperationID != c.OperationID || current.Revision != 1 {
				t.Fatal("hint failure changed root acknowledgement", current, err)
			}
			fault.failPut = false
			replay, err := q.StartRegistryCleanup(t.Context(), id, c.OperationID, c.SourceRevision, "another-operator")
			if err != nil || replay.Actor != c.Actor || replay.Epoch != c.Epoch {
				t.Fatal(replay, err)
			}
			path := (registryCleanupHint{indexScopeKey(id), c.Epoch, c.OperationID}).key()
			if _, _, err := q.Store.Get(t.Context(), path, 4096); err != nil {
				t.Fatal("replay failed to repair hint", err)
			}
		})
	}
}
func TestRegistryCleanupIndexReadsOnlyActiveCandidatesAndReconcilesMissingHint(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	for i := 0; i < 20; i++ {
		other := id
		other.TenantID = fmt.Sprint("inactive-", i)
		if err := q.AdmitInvocation(t.Context(), other); err != nil {
			t.Fatal(err)
		}
	}
	c := startRegistryCleanup(t, q, id)
	path := (registryCleanupHint{indexScopeKey(id), c.Epoch, c.OperationID}).key()
	if err := b.DeleteEntityObject(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	if p := indexedSweep(t, q); p.Attempted != 0 {
		t.Fatal("missing hint fabricated work", p)
	}
	cursor := ""
	for i := 0; i < 10; i++ {
		page, err := q.SweepRegistryCleanups(t.Context(), cursor, allowRegistryWorker)
		if err != nil || page.Failed != 0 {
			t.Fatal(page, err)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if i == 9 {
			t.Fatal("reconciliation never completed")
		}
	}
	if _, _, err := q.Store.Get(t.Context(), path, 4096); err != nil {
		t.Fatal("root repair lost unfinished operation", err)
	}
	now = now.Add(RegistryCleanupWorkerPoll)
	counted := &registryIndexFaultStore{registryCleanupBucket: b, rootReads: map[string]int{}}
	q.Store = counted
	if p := indexedSweep(t, q); p.Attempted != 1 || p.Failed != 0 {
		t.Fatal(p)
	}
	if len(counted.rootReads) != 1 || counted.rootReads["tenant-quotas/v1/"+indexScopeKey(id)] == 0 {
		t.Fatal("indexed discovery read inactive roots", counted.rootReads)
	}
	for _, prefix := range counted.lists {
		if prefix == "tenant-quotas/v1/" {
			t.Fatal("fast path listed all quota roots")
		}
	}
}
func TestRegistryCleanupIndexDelayedPruneCannotRemoveNewRotation(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	first := finishRegistryCleanup(t, q, id, startRegistryCleanup(t, q, id))
	oldPath := (registryCleanupHint{indexScopeKey(id), first.Epoch, first.OperationID}).key()
	putRegistryFixture(t, q, oldPath) // A delayed publication after completion.
	var next *RegistryCleanup
	b.beforeDelete = func(path string) {
		if path != oldPath {
			t.Fatal("unexpected prune target", path)
		}
		next = startRegistryCleanup(t, q, id)
	}
	if p := indexedSweep(t, q); p.Attempted != 0 || p.Failed != 0 {
		t.Fatal(p)
	}
	if next == nil || next.Epoch != first.Epoch+1 {
		t.Fatal(next)
	}
	newPath := (registryCleanupHint{indexScopeKey(id), next.Epoch, next.OperationID}).key()
	if _, _, err := q.Store.Get(t.Context(), newPath, 4096); err != nil {
		t.Fatal("delayed prune removed new hint", err)
	}
	current, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.OperationID != next.OperationID || current.Revision != 1 {
		t.Fatal("prune changed new operation", current, err)
	}
}
func TestRegistryCleanupIndexFutureUnknownAndUnstartedHintsAuthorizeNothing(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	other := id
	other.TenantID = "unstarted"
	if err := q.AdmitInvocation(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	preserved := []string{
		(registryCleanupHint{indexScopeKey(id), c.Epoch + 1, uuid.NewString()}).key(),
		(registryCleanupHint{indexScopeKey(id), c.Epoch, uuid.NewString()}).key(),
		(registryCleanupHint{indexScopeKey(other), 1, uuid.NewString()}).key(),
		registryCleanupIndexPrefix + "README",
	}
	for _, path := range preserved {
		putRegistryFixture(t, q, path)
	}
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	cursor := ""
	for i := 0; i < 4; i++ {
		page, err := q.SweepIndexedRegistryCleanups(t.Context(), cursor, func(durableentity.ID) bool { return false })
		if err != nil || page.Attempted != 0 || page.Failed != 0 {
			t.Fatal(page, err)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(b.deletes) != 0 {
		t.Fatal("allowlist bypassed", b.deletes)
	}
	cursor = ""
	attempts := 0
	for i := 0; i < 4; i++ {
		page, err := q.SweepIndexedRegistryCleanups(t.Context(), cursor, allowRegistryWorker)
		if err != nil || page.Failed != 0 {
			t.Fatal(page, err)
		}
		attempts += page.Attempted
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if attempts != 1 {
		t.Fatal("hint authorized unexpected work", attempts)
	}
	for _, path := range preserved {
		if _, _, err := q.Store.Get(t.Context(), path, 4096); err != nil {
			t.Fatal("unsafe hint removal", path, err)
		}
	}
	if current, err := q.RegistryCleanupStatus(t.Context(), other); err != nil || current != nil {
		t.Fatal("hint started an operation", current, err)
	}
}
func TestRegistryCleanupIndexFailureDoesNotSuppressRootFallback(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	q.Store = &registryIndexFaultStore{registryCleanupBucket: b, failPut: true, failList: true}
	if _, err := q.SweepIndexedRegistryCleanups(t.Context(), "", allowRegistryWorker); err == nil {
		t.Fatal("index listing error hidden")
	}
	if p := workerSweep(t, q); p.Attempted != 1 || p.Failed != 1 {
		t.Fatal("index error suppressed fallback work", p)
	}
	current, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.Revision != c.Revision+1 {
		t.Fatal(current, err)
	}
}
func TestRegistryCleanupIndexRejectsMalformedPagesBeforeAnyWork(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	for _, page := range []durableentity.CleanupObjects{{Keys: []string{"outside"}}, {Keys: make([]string, registryWorkerPageSize+1)}, {NextCursor: "not-progress"}} {
		b.page = &page
		if _, err := q.SweepIndexedRegistryCleanups(t.Context(), "", allowRegistryWorker); !errors.Is(err, durableentity.ErrCorrupt) {
			t.Fatal(err)
		}
	}
	b.page = nil
	if len(b.deletes) != 0 {
		t.Fatal("bad discovery deleted data")
	}
	if current, err := q.RegistryCleanupStatus(t.Context(), id); err != nil || current.Revision != c.Revision {
		t.Fatal(current, err)
	}
}
func TestRegistryCleanupIndexProbeTouchesOnlyOwnKey(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	if err := q.CheckRegistryCleanupIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(b.deletes) != 1 || !strings.HasPrefix(b.deletes[0], registryCleanupIndexPrefix+"probes/") {
		t.Fatal("probe touched cleanup data", b.deletes)
	}
	current, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.Revision != c.Revision {
		t.Fatal("probe stepped operation", current, err)
	}
}
