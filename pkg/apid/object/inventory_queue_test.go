// adr: 20261110151628

package object

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// fixedDue serves rows in order, honouring limit and, unless ignoreSkip, the
// skip list.
func fixedDue(rows []state.ObjectInventoryDue, ignoreSkip bool) inventoryDueFunc {
	return func(_ context.Context, limit int32, skip []string) ([]state.ObjectInventoryDue, error) {
		var out []state.ObjectInventoryDue
		for _, r := range rows {
			if !ignoreSkip && slices.Contains(skip, r.Bucket.ID) {
				continue
			}
			if len(out) == int(limit) {
				break
			}
			out = append(out, r)
		}
		return out, nil
	}
}

// TestInventoryQueueCapsLongListings: at most
// ObjectStorageInventoryLongListingWorkers long listings run at once, the
// other workers keep taking short buckets meanwhile, and every bucket is
// handed out exactly once.
func TestInventoryQueueCapsLongListings(t *testing.T) {
	const longs, shorts = 10, 70
	var rows []state.ObjectInventoryDue
	for i := range longs {
		rows = append(rows, state.ObjectInventoryDue{Bucket: state.ObjectBucket{ID: fmt.Sprintf("long-%02d", i)}, Long: true})
	}
	for i := range shorts {
		rows = append(rows, state.ObjectInventoryDue{Bucket: state.ObjectBucket{ID: fmt.Sprintf("short-%02d", i)}})
	}
	q := newInventoryQueue(fixedDue(rows, false), time.Now().Add(time.Minute))
	defer q.run(t.Context())()
	var mu sync.Mutex
	handed := map[string]int{}
	active, most := 0, 0
	release := make(chan struct{})
	var shortDone atomic.Int32
	var wg sync.WaitGroup
	for range api.ObjectStorageInventorySweepConcurrency {
		wg.Go(func() {
			for {
				take, ok := q.next(t.Context())
				if !ok {
					return
				}
				long := take.long
				mu.Lock()
				handed[take.bucket.ID]++
				if long {
					active++
					most = max(most, active)
				}
				mu.Unlock()
				if long {
					select {
					case <-release:
					case <-time.After(5 * time.Second):
						t.Error("short buckets starved behind long listings")
					}
					mu.Lock()
					active--
					mu.Unlock()
				} else if shortDone.Add(1) == shorts {
					close(release)
				}
				q.done(take)
			}
		})
	}
	wg.Wait()
	if most != api.ObjectStorageInventoryLongListingWorkers {
		t.Fatalf("at most %d long listings at once, want %d", most, api.ObjectStorageInventoryLongListingWorkers)
	}
	if len(handed) != longs+shorts {
		t.Fatalf("handed out %d of %d buckets", len(handed), longs+shorts)
	}
	for id, n := range handed {
		if n != 1 {
			t.Fatalf("%s handed out %d times", id, n)
		}
	}
	if q.pending() {
		t.Fatal("work left after the queue drained")
	}
}

// TestInventoryQueueStopsOnRepeatsAndDeadline: a store without a skip list
// cannot make the sweep spin on buckets it already took, and a sweep past its
// budget takes nothing more and reports the work left.
func TestInventoryQueueStopsOnRepeatsAndDeadline(t *testing.T) {
	rows := []state.ObjectInventoryDue{{Bucket: state.ObjectBucket{ID: "a"}}, {Bucket: state.ObjectBucket{ID: "b"}}}
	q := newInventoryQueue(fixedDue(rows, true), time.Now().Add(time.Minute))
	var got []string
	for {
		take, ok := q.next(t.Context())
		if !ok {
			break
		}
		got = append(got, take.bucket.ID)
		q.done(take)
	}
	if !slices.Equal(got, []string{"a", "b"}) || q.pending() {
		t.Fatal("repeats", got, q.pending())
	}

	late := newInventoryQueue(fixedDue(rows, false), time.Now().Add(-time.Second))
	if _, ok := late.next(t.Context()); ok || !late.pending() {
		t.Fatal("a sweep past its budget took work or reported none left")
	}
}

// TestInventoryQueueFillsTheTail: past the budget no long listing starts,
// short buckets keep the workers busy while an attempt that started before
// the budget is still in flight (a long listing, or the first listing of a
// bucket whose size is not known yet), and the sweep stops once none is.
func TestInventoryQueueFillsTheTail(t *testing.T) {
	rows := []state.ObjectInventoryDue{
		{Bucket: state.ObjectBucket{ID: "long-1"}, Long: true},
		{Bucket: state.ObjectBucket{ID: "unknown-1"}},
		{Bucket: state.ObjectBucket{ID: "short-1"}},
		{Bucket: state.ObjectBucket{ID: "short-2"}},
		{Bucket: state.ObjectBucket{ID: "short-3"}},
	}
	q := newInventoryQueue(fixedDue(rows, false), time.Now().Add(time.Minute))
	long, ok := q.next(t.Context())
	if !ok || !long.long || long.bucket.ID != "long-1" {
		t.Fatal("first take", long, ok)
	}
	unknown, ok := q.next(t.Context())
	if !ok || unknown.long || unknown.bucket.ID != "unknown-1" {
		t.Fatal("second take", unknown, ok)
	}
	q.deadline = time.Now().Add(-time.Second) // the budget passes while both list
	take := func(want string) {
		t.Helper()
		got, ok := q.next(t.Context())
		if !ok || got.long || got.bucket.ID != want {
			t.Fatalf("past the budget: took %+v, %v; want short %s", got, ok, want)
		}
		q.done(got)
	}
	take("short-1")
	q.done(long) // the long listing finishes; the first listing still runs
	take("short-2")
	q.done(unknown)
	if got, ok := q.next(t.Context()); ok {
		t.Fatal("handed out work after every attempt from before the budget finished", got)
	}
	if !q.pending() {
		t.Fatal("short-3 is still due")
	}
}

// blockingLister lists one 7-byte object per bucket. The slow bucket's
// listing waits until every other bucket was listed, as a long listing
// holds its worker while the rest of the sweep goes on.
type blockingLister struct {
	objectstorage.Provider
	slow     string
	others   int32
	listed   atomic.Int32
	finished chan struct{}
	timedOut atomic.Bool
}

func (p *blockingLister) ListObjects(_ context.Context, physical, _, _ string, _ int32) (objectstorage.ObjectPage, error) {
	if physical == p.slow {
		select {
		case <-p.finished:
		case <-time.After(10 * time.Second):
			p.timedOut.Store(true)
		}
	} else if p.listed.Add(1) == p.others {
		close(p.finished)
	}
	return objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "k", Size: 7}}}, nil
}

// TestInventorySweepKeepsWorkersBusyPastALongListing pins
// ADR-20261110151628 §1: a sweep used to hand out batches of 50 and wait for
// the slowest bucket of each, so one long listing idled seven workers.
func TestInventorySweepKeepsWorkersBusyPastALongListing(t *testing.T) {
	const others = 2 * api.ObjectStorageInventorySweepBatch
	st := state.NewMemStore()
	provider := &blockingLister{slow: "physical-slow", others: others, finished: make(chan struct{})}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"}, Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/external-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	// The slow bucket sorts first, so it lands in the first batch the old
	// sweep would have waited on.
	ids := []string{"00000000-0000-4000-8000-000000000001"}
	for range others {
		ids = append(ids, "f"+uuid.NewString()[1:])
	}
	for i, id := range ids {
		account, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "busy-" + uuid.NewString()[:8], Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		physical := fmt.Sprintf("physical-%d", i)
		if i == 0 {
			physical = provider.slow
		}
		b, err := st.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: id, AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: physical, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = st.ClaimObjectBucket(t.Context(), account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err = st.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
			t.Fatal(err)
		}
	}
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	var successes atomic.Int32
	more, err := svc.reconcileObjectInventories(t.Context(), func(outcome string) {
		if outcome == "success" {
			successes.Add(1)
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if provider.timedOut.Load() {
		t.Fatal("the sweep waited for the long listing before handing out the rest")
	}
	if successes.Load() != others+1 || more {
		t.Fatalf("one sweep inventoried %d of %d buckets, more %v", successes.Load(), others+1, more)
	}
}

// TestInventorySweepLoopRunsAgainWhileWorkIsDue: a sweep that left work due
// is followed by the next at once, not after the loop's minute.
func TestInventorySweepLoopRunsAgainWhileWorkIsDue(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	svc := Service{Host: receiptRecoveryHost{}}
	calls := 0
	start := time.Now()
	svc.runInventorySweepLoop(ctx, func(context.Context) (bool, error) {
		calls++
		if calls == 3 {
			cancel()
		}
		return calls < 3, nil
	}, func(time.Time) {})
	if calls != 3 || time.Since(start) > 5*time.Second {
		t.Fatalf("%d sweeps in %s", calls, time.Since(start))
	}
}
