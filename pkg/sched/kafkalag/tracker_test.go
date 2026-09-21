// adr: 198 — Kafka consumer lag as a scaling signal.
package kafkalag

import (
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

func TestTracker_SumsAcrossPartitions(t *testing.T) {
	tr := New(time.Minute)
	tr.Observe("app1", "orders/0", 100, t0)
	tr.Observe("app1", "orders/1", 250, t0)
	tr.Observe("app1", "orders/2", 50, t0)

	lag, ok := tr.LagForApp("app1", t0)
	if !ok || lag != 400 {
		t.Fatalf("LagForApp = (%d, %v), want (400, true): a topic's backlog is the sum "+
			"across partitions, since each message's high_water_mark describes only its own", lag, ok)
	}
}

func TestTracker_LastWriteWinsPerPartition(t *testing.T) {
	tr := New(time.Minute)
	tr.Observe("app1", "orders/0", 900, t0)
	// A later fetch on the same partition is a strictly better estimate of
	// what is still behind. Keeping the max would make a partition that has
	// since drained look busy forever.
	tr.Observe("app1", "orders/0", 3, t0.Add(time.Second))

	lag, ok := tr.LagForApp("app1", t0.Add(2*time.Second))
	if !ok || lag != 3 {
		t.Fatalf("LagForApp = (%d, %v), want (3, true): the newer sample must replace the older", lag, ok)
	}
}

// TestTracker_StaleSampleIsNoSignal is the freshness contract. A frozen
// reading from a wedged poller must not pin the fleet indefinitely.
func TestTracker_StaleSampleIsNoSignal(t *testing.T) {
	tr := New(time.Minute)
	tr.Observe("app1", "orders/0", 5000, t0)

	if lag, ok := tr.LagForApp("app1", t0.Add(59*time.Second)); !ok || lag != 5000 {
		t.Fatalf("within freshness = (%d, %v), want (5000, true)", lag, ok)
	}
	lag, ok := tr.LagForApp("app1", t0.Add(61*time.Second))
	if ok {
		t.Fatalf("past freshness = (%d, %v), want no signal: a frozen backlog would "+
			"otherwise hold capacity forever and bill for it", lag, ok)
	}
}

// TestTracker_PartialStalenessSumsOnlyFresh covers a mixed topic: one
// partition still producing, another long quiet. The quiet partition is
// excluded rather than carried forward, because a partition goes quiet
// precisely when it has been drained.
func TestTracker_PartialStalenessSumsOnlyFresh(t *testing.T) {
	tr := New(time.Minute)
	tr.Observe("app1", "orders/0", 800, t0)                   // goes quiet
	tr.Observe("app1", "orders/1", 7, t0.Add(90*time.Second)) // still live

	lag, ok := tr.LagForApp("app1", t0.Add(100*time.Second))
	if !ok || lag != 7 {
		t.Fatalf("LagForApp = (%d, %v), want (7, true): a drained partition contributes "+
			"zero, so excluding it is the accurate sum", lag, ok)
	}
}

func TestTracker_UnknownAppAndNilAreNoSignal(t *testing.T) {
	tr := New(time.Minute)
	if _, ok := tr.LagForApp("nobody", t0); ok {
		t.Error("unknown app reported a signal")
	}
	var nilTracker *Tracker
	if _, ok := nilTracker.LagForApp("app1", t0); ok {
		t.Error("nil tracker reported a signal")
	}
	// A nil tracker must also absorb writes: deployments with no Kafka
	// triggers never construct one.
	nilTracker.Observe("app1", "p", 1, t0)
	nilTracker.Forget("app1")
	nilTracker.Sweep(t0)
}

func TestTracker_RejectsNegativeLag(t *testing.T) {
	tr := New(time.Minute)
	tr.Observe("app1", "orders/0", -5, t0)
	if _, ok := tr.LagForApp("app1", t0); ok {
		t.Error("a negative lag was recorded; consumerLagFor never emits one and a " +
			"negative would subtract from a sibling partition's real backlog")
	}
}

func TestTracker_ForgetDropsTheApp(t *testing.T) {
	tr := New(time.Minute)
	tr.Observe("app1", "orders/0", 100, t0)
	tr.Forget("app1")
	if _, ok := tr.LagForApp("app1", t0); ok {
		t.Error("Forget left a reading behind; a removed trigger must not keep driving capacity")
	}
}

func TestTracker_SweepReclaimsExpiredEntries(t *testing.T) {
	tr := New(time.Minute)
	tr.Observe("app1", "orders/0", 100, t0)
	tr.Observe("app2", "events/0", 100, t0.Add(90*time.Second))

	tr.Sweep(t0.Add(100 * time.Second))

	tr.mu.RLock()
	_, app1Present := tr.byApp["app1"]
	_, app2Present := tr.byApp["app2"]
	tr.mu.RUnlock()
	if app1Present {
		t.Error("Sweep kept a fully expired app")
	}
	if !app2Present {
		t.Error("Sweep dropped an app with a fresh sample")
	}
}

func TestTracker_DefaultFreshness(t *testing.T) {
	if New(0).freshness != DefaultFreshness || New(-1).freshness != DefaultFreshness {
		t.Error("zero/negative freshness must select DefaultFreshness")
	}
}

// TestTracker_ConcurrentAccess pins the lock discipline: the schedd dispatch
// loop writes while the targets trigger reads, on different goroutines.
func TestTracker_ConcurrentAccess(t *testing.T) {
	tr := New(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tr.Observe("app1", "orders/0", int64(j), t0)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tr.LagForApp("app1", t0)
			}
		}()
	}
	wg.Wait()
}
