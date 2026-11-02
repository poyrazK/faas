package objectstorage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeReadStore struct {
	mu       sync.Mutex
	decision state.ObjectReadAdmission
	readErr  error
	reads    int
	flushErr error
	recorded []state.ObjectReadUsage
}

func (f *fakeReadStore) ObjectReadAdmission(_ context.Context, _, _ string, at time.Time, _ api.ObjectStoragePolicy) (state.ObjectReadAdmission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	d := f.decision
	d.Period = state.ObjectStoragePeriod(at)
	return d, f.readErr
}

func (f *fakeReadStore) RecordObjectReadUsage(_ context.Context, usage []state.ObjectReadUsage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flushErr != nil {
		return f.flushErr
	}
	f.recorded = append(f.recorded, usage...)
	return nil
}

func (f *fakeReadStore) set(fn func(*fakeReadStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time      { return c.now }
func (c *fakeClock) Add(d time.Duration) { c.now = c.now.Add(d) }
func readBucket() state.ObjectBucket {
	return state.ObjectBucket{ID: "bucket-1", AccountID: "account-1", OrgID: "org-1"}
}
func roomyDecision() state.ObjectReadAdmission {
	return state.ObjectReadAdmission{MaxRequests: 100, MaxEgressBytes: 1000, MaxAuthorizations: 100}
}

// adr: 20261030075301
func TestReadAdmitterCachesAndCounts(t *testing.T) {
	store := &fakeReadStore{decision: roomyDecision()}
	clock := &fakeClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	ctx := t.Context()
	for range 5 {
		if _, err := a.AdmitRead(ctx, readBucket()); err != nil {
			t.Fatal(err)
		}
	}
	if store.reads != 1 {
		t.Fatalf("five reads within the TTL made %d database reads", store.reads)
	}
	clock.Add(api.ObjectReadAdmissionTTL)
	admitted, err := a.AdmitRead(ctx, readBucket())
	if err != nil || store.reads != 2 {
		t.Fatalf("read after the TTL = %v, %d database reads", err, store.reads)
	}
	if err = a.ReserveEgress(admitted, 300); err != nil {
		t.Fatal(err)
	}
	if err = a.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("flush wrote %+v", store.recorded)
	}
	got := store.recorded[0]
	if got.BucketID != "bucket-1" || got.Requests != 6 || got.Authorizations != 6 || got.EgressBytes != 300 || !got.Period.Equal(state.ObjectStoragePeriod(clock.now)) {
		t.Fatalf("flushed usage = %+v", got)
	}
	if err = a.Flush(ctx); err != nil || len(store.recorded) != 1 {
		t.Fatalf("an empty flush wrote again: %v, %+v", err, store.recorded)
	}
}

func TestReadAdmitterCeilingsIncludePendingCounts(t *testing.T) {
	d := roomyDecision()
	d.Requests, d.EgressBytes = 98, 900
	store := &fakeReadStore{decision: d}
	clock := &fakeClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	ctx := t.Context()
	first, err := a.AdmitRead(ctx, readBucket())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.AdmitRead(ctx, readBucket()); err != nil {
		t.Fatal(err)
	}
	var limit *state.ObjectStorageLimitError
	if _, err = a.AdmitRead(ctx, readBucket()); !errors.As(err, &limit) || limit.Kind != "requests" || limit.Observed != 100 || !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("third read over the request ceiling = %v", err)
	}
	if err = a.ReserveEgress(first, 100); err != nil {
		t.Fatal("egress up to the ceiling refused", err)
	}
	if err = a.ReserveEgress(first, 1); !errors.As(err, &limit) || limit.Kind != "egress_bytes" {
		t.Fatalf("egress past the ceiling = %v", err)
	}
	if err = a.ReserveEgress(first, -1); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("unknown length = %v", err)
	}
}

// adr: 20261101214152 — above the plan's storage ceiling a cached read is
// still admitted for HEAD, but its body is refused and counts no egress.
func TestReadAdmitterRefusesBodiesAboveTheStorageCeiling(t *testing.T) {
	over := &state.ObjectStorageLimitError{Kind: "account_bytes", Observed: 60, Limit: 50, Cause: state.ErrObjectBudget}
	d := roomyDecision()
	d.DownloadErr = over
	store := &fakeReadStore{decision: d}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, (&fakeClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}).Now)
	read, err := a.AdmitRead(t.Context(), readBucket())
	if err != nil {
		t.Fatal("a HEAD above the ceiling was refused:", err)
	}
	if err = a.ReserveEgress(read, 0); err != nil {
		t.Fatal("an empty body above the ceiling was refused:", err)
	}
	if err = a.ReserveEgress(read, 1); !errors.Is(err, over) {
		t.Fatalf("a body above the ceiling = %v", err)
	}
	a.mu.Lock()
	pending := a.pendingLocked(readBucket().ID, read.period).EgressBytes
	a.mu.Unlock()
	if pending != 0 {
		t.Fatalf("a refused body counted %d egress bytes", pending)
	}
}

func TestReadAdmitterRefusalsAndOutages(t *testing.T) {
	store := &fakeReadStore{decision: state.ObjectReadAdmission{Err: state.ErrObjectUsageStale}}
	clock := &fakeClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	ctx := t.Context()
	if _, err := a.AdmitRead(ctx, readBucket()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("refused decision = %v", err)
	}
	if _, err := a.AdmitRead(ctx, readBucket()); !errors.Is(err, state.ErrObjectUsageStale) || store.reads != 1 {
		t.Fatalf("cached refusal = %v after %d reads", err, store.reads)
	}

	clock.Add(api.ObjectReadAdmissionTTL)
	store.set(func(f *fakeReadStore) { f.decision = roomyDecision() })
	if _, err := a.AdmitRead(ctx, readBucket()); err != nil {
		t.Fatal(err)
	}
	store.set(func(f *fakeReadStore) { f.readErr = errors.New("database unreachable") })
	clock.Add(api.ObjectReadAdmissionMaxStale - time.Second)
	if _, err := a.AdmitRead(ctx, readBucket()); err != nil {
		t.Fatal("outage within the stale window refused a read", err)
	}
	clock.Add(time.Second)
	if _, err := a.AdmitRead(ctx, readBucket()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("outage past the stale window = %v", err)
	}

	cold := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	if _, err := cold.AdmitRead(ctx, readBucket()); !errors.Is(err, state.ErrObjectUsageStale) {
		t.Fatalf("outage with nothing cached = %v", err)
	}
}

func TestReadAdmitterRefreshesAtMonthEnd(t *testing.T) {
	store := &fakeReadStore{decision: roomyDecision()}
	clock := &fakeClock{now: time.Date(2026, 10, 31, 23, 59, 59, 500_000_000, time.UTC)}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	ctx := t.Context()
	if _, err := a.AdmitRead(ctx, readBucket()); err != nil {
		t.Fatal(err)
	}
	clock.Add(time.Second)
	if _, err := a.AdmitRead(ctx, readBucket()); err != nil || store.reads != 2 {
		t.Fatalf("first read of a new month used last month's decision: %v, %d reads", err, store.reads)
	}
	if err := a.Flush(ctx); err != nil || len(store.recorded) != 2 {
		t.Fatalf("each month's counts flush separately: %v, %+v", err, store.recorded)
	}
}

func TestReadAdmitterKeepsCountsAFlushCouldNotWrite(t *testing.T) {
	d := roomyDecision()
	d.Requests = 97
	store := &fakeReadStore{decision: d, flushErr: errors.New("database unreachable")}
	clock := &fakeClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, clock.Now)
	ctx := t.Context()
	for range 2 {
		if _, err := a.AdmitRead(ctx, readBucket()); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(ctx); err == nil {
		t.Fatal("a failed flush reported success")
	}
	if _, err := a.AdmitRead(ctx, readBucket()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AdmitRead(ctx, readBucket()); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("unwritten counts were forgotten by the ceiling: %v", err)
	}
	store.set(func(f *fakeReadStore) { f.flushErr = nil })
	if err := a.Flush(ctx); err != nil || len(store.recorded) != 1 || store.recorded[0].Requests != 3 {
		t.Fatalf("retried flush = %v, %+v", err, store.recorded)
	}
	if _, err := a.AdmitRead(ctx, readBucket()); !errors.Is(err, state.ErrObjectBudget) {
		t.Fatalf("written counts were forgotten by the cached decision: %v", err)
	}
}

func TestReadAdmitterSharesOneReadAcrossConcurrentMisses(t *testing.T) {
	store := &fakeReadStore{decision: roomyDecision()}
	a := NewReadAdmitter(store, api.ObjectStoragePolicy{}, nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.AdmitRead(t.Context(), readBucket()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if store.reads != 1 {
		t.Fatalf("20 concurrent misses made %d database reads", store.reads)
	}
}

func TestAccountRequestLimiter(t *testing.T) {
	l := NewAccountRequestLimiter()
	b := readBucket()
	allowed := 0
	for range api.ObjectStorageAccountRequestBurst + 50 {
		if l.Allow(b) {
			allowed++
		}
	}
	if allowed < api.ObjectStorageAccountRequestBurst || allowed > api.ObjectStorageAccountRequestBurst+5 {
		t.Fatalf("a burst admitted %d requests", allowed)
	}
	if !l.Allow(state.ObjectBucket{ID: "other", AccountID: "account-2", OrgID: "org-2"}) {
		t.Fatal("another owner shared the first owner's bucket")
	}
	if !l.Allow(state.ObjectBucket{ID: "personal", AccountID: "account-3"}) {
		t.Fatal("a bucket without an organization was refused")
	}
}
