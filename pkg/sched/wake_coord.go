package sched

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Wake coordinator (ADR-098): per-app single-flight for the wake hot path.
//
// This is the schedd-side mirror of pkg/gateway/gate.go (the per-process
// gateway wake gate). The gateway gate only coalesces requests landing on
// one gatewayd-internal process; cron, floor, scaleup, and targets each
// call into schedd independently and bypassed the gate. Hoisting the
// per-app "wake in progress" state onto the Engine collapses all five
// wake producers into one virtual boot per parked app.
//
// Lock discipline (load-bearing — see ADR-098 §Decision):
//
//	wakeCoord.mu is a LEAF lock. It is acquired and released BEFORE
//	e.lockApp(appID) is touched. The reverse order risks permanent
//	deadlock: a pg-notify-driven Forget (C6) takes only wakeCoord.mu
//	while a leader may hold appMu. If the leader ever took wakeCoord.mu
//	under appMu, the Forget goroutine would block forever waiting for
//	appMu and the wake-coord entry would never be evicted.
//
// Phase-1..4 appMu (engine.go:911-1924) is not widened, narrowed, or
// re-entered by this code. The coordinator lives at the boundary, not
// inside the boot.
//
// Detached-ctx contract: the leader's ensure runs on
// context.Background() + TTL (default 30 s, WakeQueueTTLSeconds), so a
// cancelled triggering request cannot kill an in-flight boot that other
// follow-on callers still depend on.
//
// Error semantics:
//   - ErrQueueFull: per-app follower cap exceeded; the leader is unaffected.
//   - ErrAppDeleted: the app was deleted while we were waiting; Forget()
//     closed done with this value so followers unwind promptly.
//   - ctx.Err(): the caller's ctx was cancelled before the leader finished.
//   - nil: the wake succeeded; Outcome.Instance points at the running row.

const (
	// WakeCapDefault mirrors pkg/api/limits.go WakeQueueCap = 512. The
	// Engine-level coordinator cap is intentionally more generous than the
	// gateway's per-process gate because the coordinator is shared across
	// every wake producer on the box. We do not raise the gateway cap here;
	// the gateway remains a pre-filter (a cache in front of the authority).
	wakeCoordCap = 512

	// WakeCoordTTLDefault mirrors pkg/api/limits.go WakeQueueTTLSeconds = 30.
	// The leader's detached ensure goroutine is bounded by this; the
	// follower's await uses its own ctx.
	wakeCoordTTLDefault = 30 * time.Second
)

// ErrQueueFull is returned when the per-app follower cap is exceeded.
// Callers must convert this to a 503/ResourceExhausted response.
var ErrQueueFull = errors.New("sched: wake coordinator queue full")

// ErrAppDeleted is returned when an app is deleted while a wake is in
// flight. The Forget route (C6) closes done with this value so the
// leader's followers unwind without waiting for the TTL.
var ErrAppDeleted = errors.New("sched: app deleted")

// ErrAtCapacity is a typed sentinel the leader's EnsureWake path
// returns when Wake() reports {AtCapacity: true} — the app is at
// max_concurrency (issue #168), so no instance was created. The
// gateway uses this to distinguish "wake hit ceiling, retry against
// existing live targets" from a real admit failure. ADR-098 C11
// review fix: previously the leader set out.Instance =
// &CoordInstance{} (empty fields) which the gRPC server happily
// forwarded as a 200 with zero-valued Instance.
var ErrAtCapacity = errors.New("sched: at capacity")

// CoordOutcome is the result of a single-flight wake. On success
// Instance is non-nil and Err is nil. On failure Instance is nil and Err
// is one of ErrQueueFull / ErrAppDeleted / ctx.Err() / the leader's
// ensure error.
type CoordOutcome struct {
	// Instance is the live row schedd admitted (or the existing
	// RUNNING row the fast-path returned). Nil on the error path.
	Instance *CoordInstance
	// Err mirrors the leader's outcome. Followers inherit the leader's
	// error verbatim.
	Err error
}

// CoordInstance is the schedd-side projection of the running row that
// wake_coord.go needs to share with followers. We avoid importing the
// engine's full WakeResult here — that would create a cycle. The Engine
// populates this from its own WakeResult when it becomes the leader.
type CoordInstance struct {
	// InstanceID is the instances.id row PK.
	InstanceID string
	// NodeID is the compute_node.id the instance lives on.
	NodeID string
	// DeploymentID is the live deployment the wake landed on.
	DeploymentID string
	// WakeID is the per-wake-attempt correlation handle.
	WakeID string
	// Port is the per-deployment override port (0 = legacy 8080).
	Port int32
	// ColdBoot is true on a cold boot; false on a snapshot restore or
	// on the already-running fast path.
	ColdBoot bool
}

// wakeCoord is the per-app single-flight state. Mirrors pkg/gateway/gate.go's
// wakeCall shape but with a Shared Outcome instead of just an error.
type wakeCoord struct {
	mu       sync.Mutex
	inflight map[string]*wakeCoordCall
	cap      int
	ttl      time.Duration
}

type wakeCoordCall struct {
	// coord is a back-pointer to the owning coordinator so Complete
	// and Release can take its mutex. Without it, Complete mutates
	// the (outcome, completed) tuple outside any lock, racing with
	// Enter and Release reads. ADR-098 C12 review fix (data race
	// exposed only under -race).
	coord     *wakeCoord
	done      chan struct{}
	outcome   CoordOutcome
	waiters   int
	completed bool
}

// newWakeCoord returns a coordinator with the repo-default cap + TTL.
// The cap is intentionally identical to pkg/api/limits.go WakeQueueCap
// so a pushed-up gateway cap never silently inflates the schedd cap.
func newWakeCoord() *wakeCoord {
	return &wakeCoord{
		inflight: map[string]*wakeCoordCall{},
		cap:      wakeCoordCap,
		ttl:      wakeCoordTTLDefault,
	}
}

// Enter attempts to register a wake for appID. The returned call is the
// single-flight handle; the caller MUST eventually call Release.
//
// enter returns:
//
//   - (call, true)  — caller is the leader. It must run ensure, then
//     Complete + close(done) exactly once (use sync.Once as belt-and-braces).
//   - (call, false) — caller is a follower. It must block on call.done and
//     call Release when it returns. The leader's outcome is available via
//     call.outcome after done is closed.
//   - (nil, false)  — ErrQueueFull. The caller is the (cap+1)'th follower.
//
// cancel is the registrant's "I gave up" hook. If a cancel fires AFTER
// Release has dropped us to 0 waiters, the coordinator is expected to
// remove the entry. We rely on the leader's Complete to close done
// regardless; this hook is only for compliance with the "completed and
// un-drained" read on the gateway gate — wake_coord.go closes done
// inside Complete, so the leader's Complete itself is the cancellation.
func (c *wakeCoord) Enter(appID string) (*wakeCoordCall, bool /*leader*/, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if call, ok := c.inflight[appID]; ok {
		// ADR-098 C11 review fix: between Complete and the last
		// Release, the entry still lives in c.inflight. A new
		// caller landing here used to receive the cached prior
		// outcome — which can be a stale instance ID if the
		// instance has parked since. Detect completion and
		// fast-path a fresh entry: if the previous wake is
		// already done, drop the entry, take leadership of a
		// new wake. Followers arriving in the same window
		// (release-pending) still see the prior wake's
		// outcome — Release will drop them on a closed done.
		if call.completed {
			delete(c.inflight, appID)
		} else if call.waiters >= c.cap {
			return nil, false, ErrQueueFull
		} else {
			call.waiters++
			return call, false, nil
		}
	}
	call := &wakeCoordCall{coord: c, done: make(chan struct{}), waiters: 1}
	c.inflight[appID] = call
	return call, true, nil
}

// Complete closes done exactly once with the outcome. Safe to call
// multiple times; subsequent calls are no-ops. The Engine uses this
// from a single defer at leader entry so all five completion sites
// (engine.go:1435, 1818, 1823, 1830-1831, ~1892) cannot double-close.
//
// Sets c.completed=true so the Release() path can drop the entry once
// the last follower has drained. ADR-098 C12 review fix (data
// race): the (outcome, completed) write pair was unprotected;
// Enter reads completed under coord.mu (the "drop completed entry,
// take fresh leadership" branch), Release reads completed under
// coord.mu, but Complete wrote under no lock. Take the lock
// here too. close(c.done) is a one-way channel close and stays
// outside the lock; the lock release happens-before any
// subsequent Enter / Release that observes completed=true.
func (c *wakeCoordCall) Complete(out CoordOutcome) {
	if c.coord != nil {
		c.coord.mu.Lock()
	}
	select {
	case <-c.done:
		if c.coord != nil {
			c.coord.mu.Unlock()
		}
		// Already closed; ignore.
		return
	default:
		c.outcome = out
		c.completed = true
		// close(c.done) is one-way and serialized through the
		// channel — no race possible on the close itself, but
		// callers must observe c.outcome AFTER the unlock so
		// they see the updated value. Release the lock after
		// the close to keep the synchronization point tight.
		close(c.done)
	}
	if c.coord != nil {
		c.coord.mu.Unlock()
	}
}

// Await blocks on done or ctx cancellation. Returns the leader's
// outcome (populated before done was closed) or ctx.Err().
func (c *wakeCoordCall) Await(ctx context.Context) CoordOutcome {
	select {
	case <-c.done:
		return c.outcome
	case <-ctx.Done():
		return CoordOutcome{Err: ctx.Err()}
	}
}

// Release decrements the waiter count and removes the entry only when
// completed and the last follower is gone. Followers call this on
// every path; the leader calls it after Complete.
func (c *wakeCoord) Release(appID string, call *wakeCoordCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	call.waiters--
	if call.completed && call.waiters == 0 {
		delete(c.inflight, appID)
	}
}

// Forget evicts the entry for appID, closing done with ErrAppDeleted so
// any followers (and the leader's await) unwind without waiting for TTL.
// The pg-notify-driven app_delete_subscriber (C6) calls this.
//
// Lock discipline: takes only wakeCoord.mu; never touches appMu. The
// pg-notify goroutine has no business holding appMu; the leader, if any,
// is mid-boot and its appMu is unlocked between Phase 3 and Phase 4 per
// the engine's 4-phase contract.
//
// Idempotent: a second call after the entry is already gone is a no-op.
func (c *wakeCoord) Forget(appID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	call, ok := c.inflight[appID]
	if !ok {
		return
	}
	// Mark complete first so release() drops the entry the next time a
	// follower has finished Await. Closing done unblocks any Await that
	// is racing us.
	delete(c.inflight, appID)
	call.completed = true
	// Close done without overwriting a leader-set outcome — if a leader
	// already populated the outcome, we preserve it. Followers will see
	// the ErrAppDeleted only if the leader hadn't finished.
	if call.outcome.Err == nil {
		call.outcome = CoordOutcome{Err: ErrAppDeleted}
	}
	select {
	case <-call.done:
		// Already closed by the leader's Complete; do not double-close.
	default:
		close(call.done)
	}
}

// TTL exposes the leader-detached ctx timeout so the engine can mint it
// once at the top of the leader's goroutine and reuse it for the
// ensure call.
func (c *wakeCoord) TTL() time.Duration { return c.ttl }
