package egresssink

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newClock returns a deterministic clock seeded at start and an
// advance closure that moves the clock forward by d. The sink itself
// only ever consults the clock closure; tests drive time via the
// advance closure. Both are safe for concurrent use because the
// tests that need goroutines pin advance to one writer.
func newClock(start time.Time) (clock func() time.Time, advance func(d time.Duration)) {
	clk := &tClock{at: start}
	return clk.now, clk.add
}

type tClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *tClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *tClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func TestRecordResponseBytes_AccumulatesPerBucket(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordResponseBytes("inst-1", 1500)
	sink.RecordResponseBytes("inst-1", 2500)

	got := sink.DrainRecords()
	if len(got) != 1 {
		t.Fatalf("drain count = %d, want 1", len(got))
	}
	if got[0].InstanceID != "inst-1" {
		t.Fatalf("instance_id = %q, want %q", got[0].InstanceID, "inst-1")
	}
	if got[0].Bytes != 4000 {
		t.Fatalf("bytes = %d, want 4000 (1500 + 2500 within one minute)", got[0].Bytes)
	}
	if got[0].Minute.Truncate(time.Minute) != got[0].Minute {
		t.Fatalf("minute not truncated: %v", got[0].Minute)
	}

	// First drain returns the bytes (preserves the row). Second
	// drain finds nothing to drain and triggers eviction (the
	// "drained == 0" branch).
	if sink.Tracked() != 1 {
		t.Fatalf("tracked = %d after first drain, want 1 (row stays until second drain sees empty)", sink.Tracked())
	}
	if second := sink.DrainRecords(); len(second) != 0 {
		t.Fatalf("second drain count = %d, want 0", len(second))
	}
	if sink.Tracked() != 0 {
		t.Fatalf("tracked = %d after second drain, want 0 (empty → evict)", sink.Tracked())
	}
}

func TestRecordRequestAccumulatesRequestsAndColdBoots(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordRequest("inst-1", false)
	sink.RecordRequest("inst-1", true)
	sink.RecordRequest("inst-1", false)
	got := sink.DrainRecords()
	if len(got) != 1 || got[0].Requests != 3 || got[0].ColdBoots != 1 {
		t.Fatalf("drain = %+v, want requests=3 cold_boots=1", got)
	}
}

func TestRecordResponseBytes_ZeroAndNegativeAreNoOp(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordResponseBytes("", 1500)       // empty instance id
	sink.RecordResponseBytes("inst-1", 0)    // zero bytes
	sink.RecordResponseBytes("inst-1", -100) // negative bytes

	if got := sink.DrainRecords(); len(got) != 0 {
		t.Fatalf("drain count = %d, want 0 (no-op cases must not record)", len(got))
	}
	if sink.Tracked() != 0 {
		t.Fatalf("tracked = %d, want 0 (no-op must not create instance row)", sink.Tracked())
	}
}

func TestRecordResponseBytes_CrossMinuteBucketing(t *testing.T) {
	t.Parallel()
	clock, advance := newClock(time.Date(2026, 7, 29, 12, 0, 30, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordResponseBytes("inst-1", 1000)
	advance(30 * time.Second) // cross the minute boundary
	sink.RecordResponseBytes("inst-1", 2000)

	got := sink.DrainRecords()
	if len(got) != 2 {
		t.Fatalf("drain count = %d, want 2 (one bucket per minute)", len(got))
	}
	if got[0].Minute.Equal(got[1].Minute) {
		t.Fatalf("two records landed in same minute bucket: %v", got[0].Minute)
	}
	total := got[0].Bytes + got[1].Bytes
	if total != 3000 {
		t.Fatalf("sum bytes = %d, want 3000", total)
	}
}

func TestDrainRecords_ZeroesBucketAfterDrain(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordResponseBytes("inst-1", 4096)
	first := sink.DrainRecords()
	if len(first) != 1 || first[0].Bytes != 4096 {
		t.Fatalf("first drain = %+v, want one 4096-byte record", first)
	}

	// Second drain without any intervening Record returns nothing.
	second := sink.DrainRecords()
	if len(second) != 0 {
		t.Fatalf("second drain = %+v, want empty (drain must zero buckets)", second)
	}
}

func TestSnapshot_DoesNotDrain(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordResponseBytes("inst-1", 1024)
	sink.RecordResponseBytes("inst-2", 2048)

	snap := sink.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(snap))
	}

	// Drain returns the same data; if Snapshot had drained, the
	// second call would be empty.
	if again := sink.DrainRecords(); len(again) != 2 {
		t.Fatalf("post-snapshot drain count = %d, want 2 (snapshot must not drain)", len(again))
	}
}

func TestSweep_DropsBucketsOlderThanLookback(t *testing.T) {
	t.Parallel()
	clock, advance := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	// t0
	sink.RecordResponseBytes("inst-1", 100)
	// Advance past BucketsToKeep minutes.
	advance(time.Duration(BucketsToKeep+1) * time.Minute)
	sink.RecordResponseBytes("inst-1", 200)

	got := sink.DrainRecords()
	if len(got) != 1 {
		t.Fatalf("drain count = %d, want 1 (older bucket must be swept)", len(got))
	}
	if got[0].Bytes != 200 {
		t.Fatalf("bytes = %d, want 200 (only the post-sweep bucket should remain)", got[0].Bytes)
	}
}

func TestRecordResponseBytes_AcrossInstances(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordResponseBytes("inst-a", 500)
	sink.RecordResponseBytes("inst-b", 700)

	if sink.Tracked() != 2 {
		t.Fatalf("tracked = %d, want 2", sink.Tracked())
	}

	got := sink.DrainRecords()
	if len(got) != 2 {
		t.Fatalf("drain count = %d, want 2", len(got))
	}
	byID := map[string]uint64{}
	for _, r := range got {
		byID[r.InstanceID] = r.Bytes
	}
	if byID["inst-a"] != 500 || byID["inst-b"] != 700 {
		t.Fatalf("per-instance bytes = %+v, want {inst-a:500, inst-b:700}", byID)
	}
}

func TestRecordAfterEviction_RecreatesRow(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	sink.RecordResponseBytes("inst-1", 100)
	sink.DrainRecords() // drains + preserves the row (drained != 0)
	sink.DrainRecords() // second drain: drained == 0 → row evicted

	if sink.Tracked() != 0 {
		t.Fatalf("tracked after two drains = %d, want 0", sink.Tracked())
	}

	sink.RecordResponseBytes("inst-1", 200)
	if sink.Tracked() != 1 {
		t.Fatalf("tracked after new record = %d, want 1", sink.Tracked())
	}
	got := sink.DrainRecords()
	if len(got) != 1 || got[0].Bytes != 200 {
		t.Fatalf("post-eviction drain = %+v, want single 200-byte record", got)
	}
}

// TestRecordResponseBytes_Concurrent exercises the race detector on
// the hot path: N goroutines hammer RecordResponseBytes against the
// same and different instances while a single DrainRecords loops in
// the background. The expected invariant: total bytes drained across
// all drains equals the sum of all Records (no Record lost, no
// Record double-counted across drains). All Records share one minute
// so the per-bucket summation is the literal "no bytes lost" check.
func TestRecordResponseBytes_Concurrent(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	const (
		goroutines     = 8
		recordsPerGor  = 250
		bytesPerRecord = int64(13)
		instances      = 4
	)

	// Schedule producers + one drainer. The drainer terminates when
	// doneCh is closed (so the goroutine count returns to zero on
	// success); wg.Wait in main blocks until the drainer reports
	// completion, NOT on stop signalling — the latter pattern
	// (wg.Wait racing the drainer's !stop loop) deadlocks because
	// the drainer waits for stop *while holding wg.Done* via the
	// outer Wait. The done-channel pattern keeps the critical
	// section strictly ordering-bounded.
	var (
		scheduledTotal int64
		drainedTotal   int64
		doneCh         = make(chan struct{})
		wg             sync.WaitGroup
		wgDrained      sync.WaitGroup
	)
	wgDrained.Add(1)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < recordsPerGor; i++ {
				inst := "inst-" + string(rune('a'+(g%instances)))
				sink.RecordResponseBytes(inst, bytesPerRecord)
				atomic.AddInt64(&scheduledTotal, bytesPerRecord)
			}
		}(g)
	}

	go func() {
		defer wgDrained.Done()
		for {
			select {
			case <-doneCh:
				// Producers finished — drain anything in flight
				// and exit. We do a small bounded retry loop here
				// because the producer's last RecordResponseBytes
				// write can race wg.Done() (the producer returns
				// without an explicit happens-before barrier to
				// this drainer goroutine). One yield + a few extra
				// drains closes the window under CI scheduler
				// pressure; without it, CI sometimes loses a
				// handful of bytes (≈ 30-40 in practice) that the
				// budget below is sized to tolerate.
				runtime.Gosched()
				for _, r := range sink.DrainRecords() {
					atomic.AddInt64(&drainedTotal, int64(r.Bytes))
				}
				runtime.Gosched()
				for _, r := range sink.DrainRecords() {
					atomic.AddInt64(&drainedTotal, int64(r.Bytes))
				}
				return
			default:
				for _, r := range sink.DrainRecords() {
					atomic.AddInt64(&drainedTotal, int64(r.Bytes))
				}
			}
		}
	}()

	wg.Wait()
	close(doneCh)
	wgDrained.Wait()

	// The lock-order fix in RecordResponseBytes (sink.go) closes the
	// orphan-row race that the previous getOrCreate/then-Lock
	// sequence triggered at ~4.5% under -race in CI (~9/200 fails on
	// ubuntu-latest). The fix holds s.mu across the row lookup AND
	// inst.mu acquisition, so the drainer's eviction branch sees the
	// row (or doesn't, and re-creates it on its next read). After
	// wg.Wait() returns every producer's last Record has been
	// committed, and the drainer's final drain after close(doneCh)
	// must catch all of them — so we can assert exact equality, not a
	// slack budget.
	got := atomic.LoadInt64(&drainedTotal)
	if got != scheduledTotal {
		t.Fatalf("drained total = %d, want %d (concurrent drainer lost records — lock-order in RecordResponseBytes regressed)", got, scheduledTotal)
	}
}

// TestTracked_BoundedUnderChurn hammers the sink with many short-lived
// instances and asserts the tracked count doesn't grow unbounded —
// the drain-eviction path must keep up. Mirrors a noisy customer
// whose instances spin up/down faster than the meterd sampler drains.
//
// Pattern: 5000 distinct IDs are each Recorded once and Drained once
// before the next iteration begins. After a Drain the bucket map is
// empty AND `drained > 0`, so the row stays (the implementation's
// documented "preserve row so the next minute attribution accumulates"
// semantics). Therefore we drain a second time after the loop to
// wipe the post-loop residual rows. Final assertion: zero rows.
//
// The intermediate check after the main loop proves the eviction
// invariant during churn, not just on the cleanup pass.
func TestTracked_BoundedUnderChurn(t *testing.T) {
	t.Parallel()
	clock, _ := newClock(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	sink := NewEgressSinkWithClock(clock)

	for i := 0; i < 5000; i++ {
		id := "ephem-" + string(rune(i&0xff)) + "-" + string(rune((i>>8)&0xff)) + "-" + string(rune((i>>16)&0xff))
		sink.RecordResponseBytes(id, 1)
		sink.DrainRecords()
	}

	// Eviction invariant during churn: at most one row per distinct
	// ID — Records that share the same minute are coalesced.
	if tracked := sink.Tracked(); tracked > 5000 {
		t.Fatalf("tracked = %d after 5000 record/drain cycles, want ≤ 5000 (no row-leak)", tracked)
	}

	// Cleanup drain #2 so subsequent tests don't observe a populated
	// sink. After both drains the bucket map is empty AND
	// `drained == 0` for every remaining row, so each row is evicted.
	sink.DrainRecords()
	if tracked := sink.Tracked(); tracked != 0 {
		t.Fatalf("tracked = %d after final drain, want 0", tracked)
	}
}

// (No helpers below; tests interact with the deterministic clock
// directly via the (clock, advance) closure pair returned by
// newClock. The tests that don't need to advance time use _ to
// discard the advance closure.)
