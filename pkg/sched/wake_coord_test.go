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
		call, isLeader, err := coord.Enter(app)
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
		call, isLeader, err := coord.Enter(app)
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
		call, isLeader, err := coord.Enter(app)
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
	leaderCall, isLeader, err := coord.Enter(app)
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}

	followerCall, isLeader, err := coord.Enter(app)
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

	leaderCall, isLeader, err := coord.Enter(app)
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}
	// Fill the rest of the cap. Leader already has waiters=1; the cap
	// allows up to cap total, so we can add cap-1 more followers.
	for i := 0; i < coord.cap-1; i++ {
		_, _, err := coord.Enter(app)
		if err != nil {
			t.Fatalf("follower %d Enter: %v", i, err)
		}
	}
	// (cap+1)'th total caller must get ErrQueueFull.
	_, _, err = coord.Enter(app)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
	// Leader can still complete.
	leaderCall.Complete(CoordOutcome{Instance: &CoordInstance{InstanceID: "ins-1"}})
}

func TestWakeCoord_ForgetUnblocksFollowersWithErrAppDeleted(t *testing.T) {
	coord := newWakeCoord()
	app := "app-E"

	leaderCall, isLeader, err := coord.Enter(app)
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}

	followerCall, isLeader, err := coord.Enter(app)
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

	call, _, err := coord.Enter(app)
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
	_, isLeader, err := coord.Enter(app)
	if err != nil || !isLeader {
		t.Fatalf("leader Enter: %v / isLeader=%v", err, isLeader)
	}

	followerCall, _, err := coord.Enter(app)
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
	leader, isLeader, err := coord.Enter("app1")
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
	leader2, isLeader2, err := coord.Enter("app1")
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
