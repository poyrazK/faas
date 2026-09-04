// Whitebox test for pkg/sched/wake_coord.go — package sched per the
// repo's pattern (memory: whitebox-test-file-pattern). The coordinator
// is on the engine hot path and its lock discipline is load-bearing;
// unexported surface is in scope here.
package sched

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWakeCoord_SingleFlightLeaderCallsEnsureOnce(t *testing.T) {
	coord := newWakeCoord()
	app := "app-A"

	var (
		ensureCount int32
		seenMu      sync.Mutex
		seenOut     []CoordOutcome
		wg          sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		call, isLeader, err := coord.Enter(app, WakeFanout{})
		if err != nil {
			t.Errorf("leader Enter: %v", err)
			return
		}
		if !isLeader {
			t.Errorf("first caller must be leader")
			return
		}
		go func() {
			atomic.AddInt32(&ensureCount, 1)
			time.Sleep(50 * time.Millisecond)
			call.Complete(CoordOutcome{Instance: &CoordInstance{InstanceID: "ins-1", NodeID: "node-1", ColdBoot: true}})
		}()
		out := call.Await(context.Background())
		seenMu.Lock()
		seenOut = append(seenOut, out)
		seenMu.Unlock()
		coord.Release(app, call)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond) // ensure leader registered first
		call, isLeader, err := coord.Enter(app, WakeFanout{})
		if err != nil {
			t.Errorf("follower1 Enter: %v", err)
			return
		}
		if isLeader {
			t.Errorf("follower1 must not be leader")
			return
		}
		out := call.Await(context.Background())
		seenMu.Lock()
		seenOut = append(seenOut, out)
		seenMu.Unlock()
		coord.Release(app, call)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		call, isLeader, err := coord.Enter(app, WakeFanout{})
		if err != nil {
			t.Errorf("follower2 Enter: %v", err)
			return
		}
		if isLeader {
			t.Errorf("follower2 must not be leader")
			return
		}
		out := call.Await(context.Background())
		seenMu.Lock()
		seenOut = append(seenOut, out)
		seenMu.Unlock()
		coord.Release(app, call)
	}()
	wg.Wait()

	if got := atomic.LoadInt32(&ensureCount); got != 1 {
		t.Fatalf("ensure called %d times, want 1", got)
	}
	if len(seenOut) != 3 {
		t.Fatalf("seen %d outcomes, want 3", len(seenOut))
	}
	for i, o := range seenOut {
		if o.Err != nil {
			t.Errorf("outcome[%d]: err = %v, want nil", i, o.Err)
		}
		if o.Instance == nil || o.Instance.InstanceID != "ins-1" {
			t.Errorf("outcome[%d]: instance = %+v, want ins-1", i, o.Instance)
		}
	}
}

func TestWakeCoord_CompletedStaysParkedUntilLastFollowerDrains(t *testing.T) {
	coord := newWakeCoord()
	app := "app-C"

	// Leader + 1 follower. Leader completes; coordinator entry must
	// remain until the follower releases.
	leaderCall, isLeader, err := coord.Enter(app, WakeFanout{})
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}

	followerCall, isLeader, err := coord.Enter(app, WakeFanout{})
	if err != nil || isLeader {
		t.Fatalf("follower Enter: %v / isLeader=%v", err, isLeader)
	}

	leaderCall.Complete(CoordOutcome{Instance: &CoordInstance{InstanceID: "ins-1"}})
	// Entry must still be there: 1 follower is still parked.
	if _, ok := coord.inflight[app]; !ok {
		t.Fatalf("entry removed before follower drained")
	}
	coord.Release(app, leaderCall)
	// Leader is now gone but the follower still holds the entry.
	if _, ok := coord.inflight[app]; !ok {
		t.Fatalf("entry removed after leader release; follower still parked")
	}
	// Await followed by release.
	out := followerCall.Await(context.Background())
	if out.Err != nil {
		t.Fatalf("follower outcome err: %v", out.Err)
	}
	coord.Release(app, followerCall)
	if _, ok := coord.inflight[app]; ok {
		t.Errorf("entry should be deleted after final release")
	}
}

func TestWakeCoord_QueueFullDoesNotAffectLeader(t *testing.T) {
	coord := newWakeCoord()
	app := "app-D"

	leaderCall, isLeader, err := coord.Enter(app, WakeFanout{})
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}
	// Fill the rest of the cap. Leader already has waiters=1; the cap
	// allows up to cap total, so we can add cap-1 more followers.
	for i := 0; i < coord.cap-1; i++ {
		_, _, err := coord.Enter(app, WakeFanout{})
		if err != nil {
			t.Fatalf("follower %d Enter: %v", i, err)
		}
	}
	// (cap+1)'th total caller must get ErrQueueFull.
	_, _, err = coord.Enter(app, WakeFanout{})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
	// Leader can still complete.
	leaderCall.Complete(CoordOutcome{Instance: &CoordInstance{InstanceID: "ins-1"}})
}

func TestWakeCoord_ForgetUnblocksFollowersWithErrAppDeleted(t *testing.T) {
	coord := newWakeCoord()
	app := "app-E"

	leaderCall, isLeader, err := coord.Enter(app, WakeFanout{})
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}

	followerCall, isLeader, err := coord.Enter(app, WakeFanout{})
	if err != nil || isLeader {
		t.Fatalf("follower Enter: %v / isLeader=%v", err, isLeader)
	}

	// Forget fires while the leader is still mid-boot.
	coord.Forget(app)

	out := followerCall.Await(context.Background())
	if !errors.Is(out.Err, ErrAppDeleted) {
		t.Fatalf("follower outcome err = %v, want ErrAppDeleted", out.Err)
	}
	// Leader's await must also unwind.
	leaderOut := leaderCall.Await(context.Background())
	if !errors.Is(leaderOut.Err, ErrAppDeleted) {
		t.Fatalf("leader outcome err = %v, want ErrAppDeleted", leaderOut.Err)
	}

	// Release the followers — entry should have already been deleted by Forget.
	coord.Release(app, leaderCall)
	coord.Release(app, followerCall)
	if _, ok := coord.inflight[app]; ok {
		t.Errorf("entry should be gone after Forget")
	}
}

func TestWakeCoord_CompleteIsIdempotent(t *testing.T) {
	coord := newWakeCoord()
	app := "app-F"

	call, _, err := coord.Enter(app, WakeFanout{})
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	call.Complete(CoordOutcome{Instance: &CoordInstance{InstanceID: "ins-1"}})
	// Second call must not panic — done is already closed.
	call.Complete(CoordOutcome{Err: errors.New("ignored")})
	out := call.Await(context.Background())
	if out.Instance == nil || out.Instance.InstanceID != "ins-1" {
		t.Errorf("second Complete overwrote first outcome: %+v", out)
	}
}

func TestWakeCoord_FollowerCtxCancelReturnsCtxErr(t *testing.T) {
	coord := newWakeCoord()
	app := "app-G"

	// Leader never completes.
	_, isLeader, err := coord.Enter(app, WakeFanout{})
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}

	followerCall, _, err := coord.Enter(app, WakeFanout{})
	if err != nil {
		t.Fatalf("follower Enter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := followerCall.Await(ctx)
	if !errors.Is(out.Err, context.Canceled) {
		t.Fatalf("follower outcome err = %v, want context.Canceled", out.Err)
	}
}

func TestWakeCoord_ForgetOnAbsentAppIsNoOp(t *testing.T) {
	coord := newWakeCoord()
	// No panic on a missing entry.
	coord.Forget("never-existed")
}

// TestWakeCoord_EnterAfterCompleteStartsFreshWake (ADR-098 C11
// review fix) pins the bug where Enter returned a follower
// handle for an already-completed entry: between Complete and the
// last Release, the previous wake's entry was still in
// c.inflight. A new caller landing here got the cached prior
// outcome — possibly a stale instance ID if the underlying
// instance has parked since. The fix is to detect completion in
// Enter and take leadership of a fresh wake.
func TestWakeCoord_EnterAfterCompleteStartsFreshWake(t *testing.T) {
	coord := newWakeCoord()
	// Leader wakes, completes, and is the only entry.
	leader, isLeader, err := coord.Enter("app1", WakeFanout{})
	if err != nil || !isLeader {
		t.Fatalf("first Enter err=%v isLeader=%v", err, isLeader)
	}
	leader.Complete(CoordOutcome{
		Instance: &CoordInstance{InstanceID: "parked-1"},
	})
	// Pre-fix: a new caller would land here as a follower and
	// receive {InstanceID: "parked-1"} — possibly stale. Post-fix:
	// Enter detects completed=true, drops the entry, returns a new
	// (true) leader.
	leader2, isLeader2, err := coord.Enter("app1", WakeFanout{})
	if err != nil {
		t.Fatalf("second Enter err=%v", err)
	}
	if !isLeader2 {
		t.Fatalf("second Enter: expected to take leadership of fresh entry (completed entry should be replaced), got isLeader=false (cached follower)")
	}
	if leader2 == leader {
		t.Fatalf("second Enter returned the same completed wakeCoordCall; expected a fresh one")
	}
	// The fresh entry's outcome is the zero value until its
	// leader calls Complete. Verify Await with a short timeout
	// returns the ctx error (done not yet closed) rather than
	// the stale outcome from the prior wake. Then Complete and
	// Await again to confirm the fresh entry produces its own
	// outcome, not a cached prior.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if out := leader2.Await(ctx); !errors.Is(out.Err, context.DeadlineExceeded) {
		t.Fatalf("fresh entry's pre-Complete Await = %+v, want DeadlineExceeded (pre-Complete done is closed)", out)
	}
	leader2.Complete(CoordOutcome{Instance: &CoordInstance{InstanceID: "fresh"}})
	out := leader2.Await(context.Background())
	if out.Err != nil {
		t.Fatalf("fresh entry outcome err = %v", out.Err)
	}
	if out.Instance == nil || out.Instance.InstanceID != "fresh" {
		t.Fatalf("fresh entry outcome = %+v, want InstanceID=fresh", out.Instance)
	}
	// Drain.
	coord.Release("app1", leader)
	coord.Release("app1", leader2)
}

// TestWakeFanout_Wants pins the admission rule in isolation. The rule is
// "the wakes already running cannot absorb the callers already waiting",
// not "any waiter starts a new wake" — the latter would wake 50 VMs for a
// 50-request burst that one instance already serves.
func TestWakeFanout_Wants(t *testing.T) {
	f := WakeFanout{MaxInFlight: 20, PerVM: 80}
	cases := []struct {
		name              string
		inFlight, waiting int
		want              bool
	}{
		{"one wake absorbs a small burst", 1, 50, false},
		{"one wake exactly at its capacity", 1, 80, false},
		{"demand exceeds one wake", 1, 81, true},
		{"demand exceeds two wakes", 2, 161, true},
		{"two wakes still absorb it", 2, 160, false},
		{"at the app instance ceiling", 20, 100000, false},
	}
	for _, tc := range cases {
		if got := f.wants(tc.inFlight, tc.waiting); got != tc.want {
			t.Errorf("%s: wants(inFlight=%d, waiting=%d) = %v, want %v",
				tc.name, tc.inFlight, tc.waiting, got, tc.want)
		}
	}
}

// TestWakeFanout_ZeroPolicyIsStrictSingleFlight pins the fail-safe. Any
// caller that cannot resolve the app's limits passes the zero value, and
// must get the original one-wake-per-app behaviour rather than an
// unbounded fan-out.
func TestWakeFanout_ZeroPolicyIsStrictSingleFlight(t *testing.T) {
	for _, f := range []WakeFanout{
		{},
		{MaxInFlight: 20},           // no PerVM
		{PerVM: 80},                 // no MaxInFlight
		{MaxInFlight: -1, PerVM: 8}, // nonsense
	} {
		if f.wants(1, 1_000_000) {
			t.Errorf("WakeFanout%+v fanned out; an unresolved policy must stay single-flight", f)
		}
	}
}

// TestWakeCoord_FansOutWhenDemandExceedsOneInstance is the regression
// test for the cold-burst defect.
//
// Before: the coordinator was strict single-flight, so a burst against a
// parked app woke exactly ONE instance no matter how many callers piled
// up. Everything past that instance's own concurrency ceiling waited for
// a slot that was never coming and timed out — measured 9.5% success for
// 1000 requests at 200 concurrency against a cold app, versus 99.9% for
// the same app pre-warmed.
func TestWakeCoord_FansOutWhenDemandExceedsOneInstance(t *testing.T) {
	coord := newWakeCoord()
	const app = "app-burst"
	fanout := WakeFanout{MaxInFlight: 5, PerVM: 2}

	// First caller leads.
	if _, isLeader, err := coord.Enter(app, fanout); err != nil || !isLeader {
		t.Fatalf("first Enter: leader=%v err=%v, want leader", isLeader, err)
	}
	// Within the leader's capacity (PerVM=2): follower, not a new wake.
	if _, isLeader, err := coord.Enter(app, fanout); err != nil || isLeader {
		t.Fatalf("second Enter: leader=%v err=%v, want follower (one wake still absorbs it)", isLeader, err)
	}
	// Third caller exceeds 1 wake * 2 per VM → a second wake starts.
	if _, isLeader, err := coord.Enter(app, fanout); err != nil || !isLeader {
		t.Fatalf("third Enter: leader=%v err=%v, want a SECOND leader; without fan-out the burst is stuck on one instance", isLeader, err)
	}
	if got := len(coord.inflight[app]); got != 2 {
		t.Fatalf("in-flight wakes = %d, want 2", got)
	}
}

// TestWakeCoord_FanoutStopsAtInstanceCeiling pins invariant §6.2-1 at the
// coordinator: a burst must never start more wakes than the app is
// allowed instances, however many callers queue up.
func TestWakeCoord_FanoutStopsAtInstanceCeiling(t *testing.T) {
	coord := newWakeCoord()
	const app = "app-ceiling"
	fanout := WakeFanout{MaxInFlight: 3, PerVM: 1}

	leaders := 0
	for i := 0; i < 50; i++ {
		if _, isLeader, err := coord.Enter(app, fanout); err != nil {
			t.Fatalf("Enter %d: %v", i, err)
		} else if isLeader {
			leaders++
		}
	}
	if leaders != 3 {
		t.Errorf("started %d wakes, want 3 (the app's max_concurrency)", leaders)
	}
	if got := len(coord.inflight[app]); got != 3 {
		t.Errorf("in-flight = %d, want 3", got)
	}
}

// TestWakeCoord_FollowersSpreadAcrossWakes pins that followers join the
// least-loaded in-flight wake. Piling every follower onto the first one
// would trip the per-call queue cap while its siblings sat idle, turning
// a successful fan-out into ErrQueueFull.
func TestWakeCoord_FollowersSpreadAcrossWakes(t *testing.T) {
	coord := newWakeCoord()
	const app = "app-spread"
	fanout := WakeFanout{MaxInFlight: 2, PerVM: 1}

	// Two leaders (second admitted once demand passes 1*1).
	if _, l1, _ := coord.Enter(app, fanout); !l1 {
		t.Fatal("first caller should lead")
	}
	if _, l2, _ := coord.Enter(app, fanout); !l2 {
		t.Fatal("second caller should start a second wake")
	}
	// At the ceiling now; the next callers are followers and must be
	// distributed rather than all landing on the first wake.
	for i := 0; i < 6; i++ {
		if _, isLeader, err := coord.Enter(app, fanout); err != nil || isLeader {
			t.Fatalf("caller %d: leader=%v err=%v, want follower", i, isLeader, err)
		}
	}
	calls := coord.inflight[app]
	if len(calls) != 2 {
		t.Fatalf("in-flight = %d, want 2", len(calls))
	}
	for i, c := range calls {
		if c.waiters != 4 {
			t.Errorf("wake %d has %d waiters, want 4 evenly spread; lopsided joins hit the queue cap early", i, c.waiters)
		}
	}
}

// TestWakeCoord_ReleaseDropsOnlyItsOwnWake pins that completing one wake
// of a fanned-out set does not evict its siblings — that would strand
// their followers on an entry the coordinator no longer tracks.
func TestWakeCoord_ReleaseDropsOnlyItsOwnWake(t *testing.T) {
	coord := newWakeCoord()
	const app = "app-release"
	fanout := WakeFanout{MaxInFlight: 3, PerVM: 1}

	c1, _, _ := coord.Enter(app, fanout)
	c2, _, _ := coord.Enter(app, fanout)
	if len(coord.inflight[app]) != 2 {
		t.Fatalf("setup: in-flight = %d, want 2", len(coord.inflight[app]))
	}
	c1.Complete(CoordOutcome{})
	coord.Release(app, c1)
	if got := len(coord.inflight[app]); got != 1 {
		t.Fatalf("after releasing one wake, in-flight = %d, want 1 (sibling must survive)", got)
	}
	c2.Complete(CoordOutcome{})
	coord.Release(app, c2)
	if _, ok := coord.inflight[app]; ok {
		t.Error("entry should be gone once every wake has drained")
	}
}
