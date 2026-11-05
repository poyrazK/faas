package objectstorage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type blockingReadUsageStore struct {
	base             *fakeReadStore
	started          chan struct{}
	release          chan struct{}
	commitBeforeWait bool
}

func (s *blockingReadUsageStore) ObjectReadAdmission(ctx context.Context, account, bucket string, at time.Time, p api.ObjectStoragePolicy) (state.ObjectReadAdmission, error) {
	return s.base.ObjectReadAdmission(ctx, account, bucket, at, p)
}

func (s *blockingReadUsageStore) RecordObjectReadUsage(ctx context.Context, usage []state.ObjectReadUsage) error {
	if s.commitBeforeWait {
		s.base.set(func(f *fakeReadStore) {
			for _, u := range usage {
				f.decision.Requests += u.Requests
				f.decision.EgressBytes += u.EgressBytes
				f.decision.Authorizations += u.Authorizations
			}
		})
	}
	close(s.started)
	select {
	case <-s.release:
		return s.base.RecordObjectReadUsage(ctx, usage)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestReadAdmitterRetainsInflightFlushBudgets(t *testing.T) {
	for _, budget := range []string{"requests", "authorizations", "egress"} {
		t.Run(budget, func(t *testing.T) {
			d := roomyDecision()
			if budget == "requests" {
				d.Requests = d.MaxRequests - 1
			} else if budget == "authorizations" {
				d.Authorizations = d.MaxAuthorizations - 1
			}
			s := &blockingReadUsageStore{base: &fakeReadStore{decision: d}, started: make(chan struct{}), release: make(chan struct{})}
			a := NewReadAdmitter(s, api.ObjectStoragePolicy{}, nil)
			r, err := a.AdmitRead(t.Context(), readBucket())
			if err != nil {
				t.Fatal(err)
			}
			if err = a.ReserveEgress(r, d.MaxEgressBytes); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- a.Flush(t.Context()) }()
			<-s.started
			defer func() { close(s.release); <-done }()
			if budget == "egress" {
				err = a.ReserveEgress(r, 1)
			} else {
				_, err = a.AdmitRead(t.Context(), readBucket())
			}
			if !errors.Is(err, state.ErrObjectBudget) {
				t.Fatalf("%s budget in an in-flight flush was available again: %v", budget, err)
			}
		})
	}
}

func TestReadAdmitterRefreshDuringFlushDoesNotDoubleCount(t *testing.T) {
	d := roomyDecision()
	d.MaxRequests = 3
	s := &blockingReadUsageStore{base: &fakeReadStore{decision: d}, started: make(chan struct{}), release: make(chan struct{}), commitBeforeWait: true}
	clock := &fakeClock{now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	a := NewReadAdmitter(s, api.ObjectStoragePolicy{}, clock.Now)
	if _, err := a.AdmitRead(t.Context(), readBucket()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Flush(t.Context()) }()
	<-s.started
	clock.Add(api.ObjectReadAdmissionTTL)
	// A stalled flush cannot stop an otherwise cached read. Refreshing waits
	// at most the normal read timeout and uses the admitting stale decision.
	_, err := a.AdmitRead(t.Context(), readBucket())
	close(s.release)
	if flushErr := <-done; flushErr != nil {
		t.Fatal(flushErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if s.base.reads != 1 {
		t.Fatal("a database snapshot overlapped the flush")
	}
	clock.Add(api.ObjectReadAdmissionTTL)
	if _, err := a.AdmitRead(t.Context(), readBucket()); err != nil {
		t.Fatalf("third request under a three-request ceiling was refused: %v", err)
	}
	if s.base.reads != 2 {
		t.Fatal("refresh did not resume after the flush")
	}
	if _, err := a.AdmitRead(t.Context(), readBucket()); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("fourth request bypassed the ceiling: %v", err)
	}
}

func TestReadAdmitterSharesOwnerBudgetsAcrossBuckets(t *testing.T) {
	for _, org := range []bool{false, true} {
		for _, flushed := range []bool{false, true} {
			for _, budget := range []string{"requests", "authorizations", "egress"} {
				t.Run(budget+"/"+map[bool]string{false: "personal", true: "organization"}[org]+"/"+map[bool]string{false: "pending", true: "flushed"}[flushed], func(t *testing.T) {
					testSharedReadBudget(t, org, flushed, budget)
				})
			}
		}
	}
}

func testSharedReadBudget(t *testing.T, org, flushed bool, budget string) {
	t.Helper()
	d := roomyDecision()
	switch budget {
	case "requests":
		d.Requests = d.MaxRequests - 1
	case "authorizations":
		d.Authorizations = d.MaxAuthorizations - 1
	case "egress":
		d.EgressBytes = d.MaxEgressBytes - 1
	}
	store := &fakeReadStore{decision: d}
	if org {
		store.decision.SubjectID = readBucket().OrgID
	}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, nil)
	b := readBucket()
	if !org {
		b.OrgID = ""
	}
	r, err := a.AdmitRead(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if budget == "egress" {
		if err = a.ReserveEgress(r, 1); err != nil {
			t.Fatal(err)
		}
	}
	if flushed {
		if err = a.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	b.ID = "bucket-2"
	if org {
		b.AccountID = "other-member"
	}
	if _, err = a.AdmitRead(t.Context(), b); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("another bucket reused the owner's final %s allowance: %v", budget, err)
	}
	b.ID, b.AccountID, b.OrgID = "bucket-3", "other-account", "other-org"
	store.set(func(f *fakeReadStore) { f.decision.SubjectID = "" })
	if _, err = a.AdmitRead(t.Context(), b); err != nil {
		t.Fatal("an independent owner shared the exhausted budget:", err)
	}
}

func TestReadAdmitterKeepsUnenrolledOrganizationAccountsIndependent(t *testing.T) {
	d := roomyDecision()
	d.Requests = d.MaxRequests - 1
	a := NewReadAdmitter(&fakeReadStore{decision: d}, api.ObjectStoragePolicy{}, nil)
	b := readBucket()
	if _, err := a.AdmitRead(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	// The store returns account billing subjects until company enrollment,
	// even though both buckets already carry the same organization ID.
	b.ID, b.AccountID = "bucket-2", "other-member"
	if _, err := a.AdmitRead(t.Context(), b); err != nil {
		t.Fatal("two account-billed members shared a budget before enrollment:", err)
	}
}

func TestReadAdmitterConcurrentBucketsRespectOwnerCeiling(t *testing.T) {
	store := &fakeReadStore{decision: roomyDecision()}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, nil)
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() {
			b := readBucket()
			b.ID = string(rune('a' + i%10))
			r, err := a.AdmitRead(t.Context(), b)
			if errors.Is(err, state.ErrObjectBudget) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			admitted.Add(1)
			if err = a.ReserveEgress(r, 10); err != nil {
				t.Error(err)
			}
			if i%10 == 0 {
				if err = a.Flush(t.Context()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if got := admitted.Load(); got != store.decision.MaxRequests {
		t.Fatalf("admitted %d requests across buckets; want %d", got, store.decision.MaxRequests)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var requests, authorizations, egress int64
	for _, u := range store.recorded {
		requests += u.Requests
		authorizations += u.Authorizations
		egress += u.EgressBytes
	}
	if requests != 100 || authorizations != 100 || egress != 1000 {
		t.Fatalf("flushed %d requests, %d authorizations, %d egress bytes", requests, authorizations, egress)
	}
}

func TestReadAdmitterReservesLateEgressAfterDecisionEviction(t *testing.T) {
	a := NewReadAdmitter(&fakeReadStore{decision: roomyDecision()}, api.ObjectStoragePolicy{}, nil)
	r, err := a.AdmitRead(t.Context(), readBucket())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ReserveEgress(r, 600); err != nil {
		t.Fatal(err)
	}
	if err = a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	clear(a.entries)
	a.mu.Unlock()
	b := readBucket()
	b.ID = "bucket-2"
	other, err := a.AdmitRead(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ReserveEgress(other, 400); err != nil {
		t.Fatal(err)
	}
	if err = a.ReserveEgress(r, 1); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("an old transfer forgot the owner's other egress after cache eviction: %v", err)
	}
}

func TestReadAdmitterKeepsOwnerUntilUnwrittenUsageSettles(t *testing.T) {
	store := &fakeReadStore{decision: roomyDecision(), flushErr: errors.New("database unreachable")}
	clock := &fakeClock{now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	r, err := a.AdmitRead(t.Context(), readBucket())
	if err != nil {
		t.Fatal(err)
	}
	clock.Add(api.MaxObjectTransferTimeout + time.Second)
	if err = a.Flush(t.Context()); err == nil {
		t.Fatal("failed flush reported success")
	}
	if len(a.owners) != 1 {
		t.Fatal("unwritten usage lost its owner after the transfer lifetime")
	}
	store.set(func(f *fakeReadStore) { f.flushErr = nil })
	if err = a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	clock.Add(api.ObjectReadAdmissionMaxStale)
	if err = a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(a.owners) != 0 {
		t.Fatal("a settled, idle owner was retained past the transfer lifetime")
	}
	if err = a.ReserveEgress(r, 1); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("an expired transfer revived an evicted owner's counters: %v", err)
	}
}

func TestReadAdmitterFlushesPendingUsageAcrossOwnerCutover(t *testing.T) {
	store := &fakeReadStore{decision: roomyDecision()}
	clock := &fakeClock{now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	before, err := a.AdmitRead(t.Context(), readBucket())
	if err != nil {
		t.Fatal(err)
	}
	store.set(func(f *fakeReadStore) { f.decision.SubjectID = readBucket().OrgID })
	clock.Add(api.ObjectReadAdmissionTTL)
	after, err := a.AdmitRead(t.Context(), readBucket())
	if err != nil {
		t.Fatal(err)
	}
	if before.owner == after.owner {
		t.Fatal("the organization enrollment did not change the billing owner")
	}
	if err = a.ReserveEgress(before, 10); err != nil {
		t.Fatal(err)
	}
	if err = a.ReserveEgress(after, 20); err != nil {
		t.Fatal(err)
	}
	if err = a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if before.owner.unwritten != (readUsageCounts{}) || after.owner.unwritten != (readUsageCounts{}) {
		t.Fatal("a cutover mixed two owners' pending usage")
	}
}
