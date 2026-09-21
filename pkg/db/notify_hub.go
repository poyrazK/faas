package db

// Notify hub (ADR-190, decision 4): one LISTEN connection per daemon.
//
// Subscribe parks one pooled connection per call. Production daemons
// subscribe from many places (apid 9, schedd 12, gatewayd-internal 4,
// vmmd 3, …), so a node ran ~46 permanently parked LISTEN connections
// against max_connections=100 and on 2026-09-12 every daemon on fsn-1
// died with SQLSTATE 53300. DaemonMaxConnections had to be sized
// around parked connections rather than around work.
//
// The hub multiplexes every SubscribeWithReconnect call on a pool onto
// a single LISTEN connection. Subscribers keep the exact channel shape
// they have today; the hub owns reconnect, re-LISTEN, and fan-out.
//
// Two contracts are preserved deliberately:
//
//   - Fail-fast at boot: the first subscriber on a pool acquires the
//     connection synchronously and returns the error if Postgres is
//     unreachable, as Subscribe does today.
//   - LISTEN is active when subscribe returns: daemons subscribe first
//     and then drain durable tables (pkg/sched/loop.go), so a channel
//     added to a running hub blocks until the loop has applied it.
//
// One contract changes: a slow subscriber no longer applies
// backpressure to the connection. Every consumer treats LISTEN as a
// wake-up over a table source of truth with a safety ticker (the loop
// documents this per channel), so an overflowing subscriber drops the
// newest notification, counts it, and relies on that ticker rather
// than stalling every other subscriber on the daemon.
//
// FAAS_DB_NOTIFY_HUB=0 restores one connection per subscriber.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NotifyHubEnv is the kill switch. Any value other than "0" (including
// unset) routes SubscribeWithReconnect through the per-pool hub.
//
// The switch also selects the daemon's pool budget
// (db.DaemonMaxConnectionsNotifyHubDisabled), so flipping it stays a safe
// rollback even once the hub-on budgets are lowered to reflect the single
// parked connection the hub actually uses.
const NotifyHubEnv = "FAAS_DB_NOTIFY_HUB"

// legacySubscribeAcquireTimeout bounds the first connection acquire on the
// FAAS_DB_NOTIFY_HUB=0 path. Long enough that a slow-but-working Postgres
// still subscribes; short enough that a pool which can never seat every
// subscriber fails the unit's start rather than hanging in `activating`
// until systemd's TimeoutStartSec. Only the initial acquire is bounded —
// the reconnect loop retries indefinitely by design.
//
// A var rather than a const so the exhaustion test can shorten it: that test
// must actually reach the timeout, and paying 15 s per run in the pg shard to
// re-measure a constant is not worth it.
var legacySubscribeAcquireTimeout = 15 * time.Second

const (
	// hubSubscriberBuffer is the per-subscriber fan-out buffer. 1024
	// covers any realistic notify burst (the largest producers emit a
	// few hundred per second under load) while bounding memory.
	hubSubscriberBuffer = 1024
	// hubApplyTimeout bounds how long a subscribe on a running hub
	// waits for its LISTEN to be applied. Longer than the 5 s
	// reconnect cap so one reconnect cycle cannot fail a boot.
	hubApplyTimeout = 15 * time.Second
	hubMinBackoff   = 100 * time.Millisecond
	hubMaxBackoff   = 5 * time.Second
	// hubDropLogEvery bounds the overflow warning per subscriber.
	hubDropLogEvery = time.Minute
)

// NotifyHubObserver receives hub health events. wire.OpsMetrics
// implements it; RegisterDefaultOps installs it via SetNotifyHubObserver
// so every daemon's /metrics carries the two counters without
// per-daemon wiring.
type NotifyHubObserver interface {
	HubReconnect()
	HubDropped(channel string)
}

var (
	hubObserverMu sync.RWMutex
	hubObserver   NotifyHubObserver
)

// SetNotifyHubObserver installs the metrics sink. nil clears it.
func SetNotifyHubObserver(o NotifyHubObserver) {
	hubObserverMu.Lock()
	hubObserver = o
	hubObserverMu.Unlock()
}

func observeHub(fn func(NotifyHubObserver)) {
	hubObserverMu.RLock()
	o := hubObserver
	hubObserverMu.RUnlock()
	if o != nil {
		fn(o)
	}
}

func notifyHubEnabled() bool { return os.Getenv(NotifyHubEnv) != "0" }

var hubs sync.Map // *pgxpool.Pool -> *notifyHub

func hubFor(pool *pgxpool.Pool, log *slog.Logger) *notifyHub {
	if h, ok := hubs.Load(pool); ok {
		return h.(*notifyHub)
	}
	h := newNotifyHub(pool, log)
	actual, _ := hubs.LoadOrStore(pool, h)
	return actual.(*notifyHub)
}

type hubSubscriber struct {
	channels map[string]struct{}
	out      chan Notification
	dropped  atomic.Int64
	lastDrop atomic.Int64 // unix nanos of the last overflow warning
}

func (s *hubSubscriber) wants(channel string) bool {
	_, ok := s.channels[channel]
	return ok
}

type notifyHub struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	mu      sync.Mutex
	subs    map[*hubSubscriber]struct{}
	wanted  map[string]int // channel -> subscriber refcount
	gen     uint64         // bumped on every wanted change
	applied uint64         // last gen the loop has LISTENed
	cond    *sync.Cond     // signalled when applied advances or the loop fails
	lastErr error          // most recent connection error while gen > applied
	running bool
	cancel  context.CancelFunc
	wake    chan struct{} // 1-buffered; nudges the loop to re-sync LISTENs

	reconnects atomic.Int64
}

func newNotifyHub(pool *pgxpool.Pool, log *slog.Logger) *notifyHub {
	h := &notifyHub{
		pool:   pool,
		log:    log,
		subs:   map[*hubSubscriber]struct{}{},
		wanted: map[string]int{},
		wake:   make(chan struct{}, 1),
	}
	h.cond = sync.NewCond(&h.mu)
	return h
}

// NotifyHubStats is a point-in-time view for tests and diagnostics.
type NotifyHubStats struct {
	Subscribers int
	Channels    int
	Reconnects  int64
	Dropped     int64
	Running     bool
}

// NotifyHubStatsFor reports the hub state for pool (zero if none).
func NotifyHubStatsFor(pool *pgxpool.Pool) NotifyHubStats {
	v, ok := hubs.Load(pool)
	if !ok {
		return NotifyHubStats{}
	}
	h := v.(*notifyHub)
	h.mu.Lock()
	defer h.mu.Unlock()
	var dropped int64
	for s := range h.subs {
		dropped += s.dropped.Load()
	}
	return NotifyHubStats{
		Subscribers: len(h.subs),
		Channels:    len(h.wanted),
		Reconnects:  h.reconnects.Load(),
		Dropped:     dropped,
		Running:     h.running,
	}
}

// subscribe registers channels for a new subscriber and returns its
// delivery channel. The channel closes when ctx is cancelled.
func (h *notifyHub) subscribe(ctx context.Context, channels []string) (<-chan Notification, error) {
	sub := &hubSubscriber{channels: map[string]struct{}{}, out: make(chan Notification, hubSubscriberBuffer)}
	for _, c := range channels {
		sub.channels[c] = struct{}{}
	}

	h.mu.Lock()
	if !h.running {
		// First subscriber: acquire synchronously so an unreachable
		// database at boot fails here, matching Subscribe.
		if err := h.startLocked(ctx, channels); err != nil {
			h.mu.Unlock()
			return nil, err
		}
	}
	h.subs[sub] = struct{}{}
	changed := false
	for c := range sub.channels {
		h.wanted[c]++
		if h.wanted[c] == 1 {
			changed = true
		}
	}
	var waitGen uint64
	if changed {
		h.gen++
		waitGen = h.gen
		h.lastErr = nil
		h.nudge()
	}
	h.mu.Unlock()

	if changed {
		if err := h.waitApplied(ctx, waitGen); err != nil {
			h.unsubscribe(sub)
			return nil, err
		}
	}
	go func() {
		<-ctx.Done()
		h.unsubscribe(sub)
	}()
	return sub.out, nil
}

// startLocked acquires the hub connection and LISTENs the initial
// channel set synchronously under the first subscriber's ctx (so an
// unreachable database fails that subscribe), then starts the serve
// loop under the hub's own lifetime context: the connection must
// outlive any single subscriber. Caller holds h.mu.
func (h *notifyHub) startLocked(ctx context.Context, initial []string) error {
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("db: notify hub acquire listener: %w", err)
	}
	listened := map[string]struct{}{}
	for _, c := range initial {
		if _, err := conn.Exec(ctx, "LISTEN "+quoteIdent(c)); err != nil {
			conn.Release()
			return fmt.Errorf("db: notify hub LISTEN %s: %w", c, err)
		}
		listened[c] = struct{}{}
	}
	hubCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	h.running = true
	h.cancel = cancel
	go h.run(hubCtx, conn, listened)
	return nil
}

func (h *notifyHub) nudge() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// waitApplied blocks until the loop has LISTENed generation gen, the
// loop reports a connection error, ctx ends, or hubApplyTimeout.
func (h *notifyHub) waitApplied(ctx context.Context, gen uint64) error {
	deadline := time.Now().Add(hubApplyTimeout)
	timer := time.AfterFunc(hubApplyTimeout, func() { h.cond.Broadcast() })
	defer timer.Stop()
	stop := context.AfterFunc(ctx, func() { h.cond.Broadcast() })
	defer stop()
	h.mu.Lock()
	defer h.mu.Unlock()
	for h.applied < gen {
		if h.lastErr != nil {
			return fmt.Errorf("db: notify hub LISTEN not applied: %w", h.lastErr)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("db: notify hub LISTEN not applied within " + hubApplyTimeout.String())
		}
		h.cond.Wait()
	}
	return nil
}

func (h *notifyHub) unsubscribe(sub *hubSubscriber) {
	h.mu.Lock()
	if _, ok := h.subs[sub]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.subs, sub)
	changed := false
	for c := range sub.channels {
		h.wanted[c]--
		if h.wanted[c] <= 0 {
			delete(h.wanted, c)
			changed = true
		}
	}
	if len(h.subs) == 0 && h.running {
		h.running = false
		h.cancel()
		h.cancel = nil
	} else if changed {
		h.gen++
		h.nudge()
	}
	h.mu.Unlock()
	close(sub.out)
}

// run owns the LISTEN connection for the hub's lifetime: serve until
// a fatal connection error, then reacquire with backoff and re-LISTEN
// the full wanted set. Exits when ctx is cancelled (last subscriber
// left).
func (h *notifyHub) run(ctx context.Context, conn *pgxpool.Conn, listened map[string]struct{}) {
	backoff := hubMinBackoff
	for {
		if conn != nil {
			err := h.serve(ctx, conn, listened)
			conn.Release()
			conn = nil
			if ctx.Err() != nil {
				return
			}
			h.reconnects.Add(1)
			observeHub(func(o NotifyHubObserver) { o.HubReconnect() })
			h.mu.Lock()
			h.lastErr = err
			h.cond.Broadcast()
			h.mu.Unlock()
			if h.log != nil {
				h.log.Warn("db: notify hub LISTEN connection lost; reconnecting", "err", err, "backoff", backoff.String())
			}
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		c, err := h.pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.mu.Lock()
			h.lastErr = err
			h.cond.Broadcast()
			h.mu.Unlock()
			backoff = min(backoff*2, hubMaxBackoff)
			continue
		}
		listened = map[string]struct{}{}
		if err := h.syncListens(ctx, c, listened); err != nil {
			c.Release()
			h.mu.Lock()
			h.lastErr = err
			h.cond.Broadcast()
			h.mu.Unlock()
			backoff = min(backoff*2, hubMaxBackoff)
			continue
		}
		backoff = hubMinBackoff
		if h.log != nil {
			h.log.Info("db: notify hub LISTEN re-subscribed", "channels", len(listened))
		}
		conn = c
	}
}

// serve waits for notifications on conn and fans them out. A wake on
// h.wake cancels the wait so the LISTEN set can be re-synced on the
// same connection; pgx surfaces that cancel as a timeout and keeps
// the connection open. Returns on a fatal connection error or ctx.
func (h *notifyHub) serve(ctx context.Context, conn *pgxpool.Conn, listened map[string]struct{}) error {
	// Apply anything that changed while the connection was being set up.
	if err := h.syncListens(ctx, conn, listened); err != nil {
		return err
	}
	for {
		waitCtx, cancel := context.WithCancel(ctx)
		stopWatch := make(chan struct{})
		go func() {
			select {
			case <-h.wake:
				cancel()
			case <-stopWatch:
			}
		}()
		n, err := conn.Conn().WaitForNotification(waitCtx)
		close(stopWatch)
		cancel()
		if err == nil {
			h.dispatch(decodeNotification(n.Channel, n.Payload))
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.Canceled) && waitCtx.Err() != nil {
			// Woken to re-sync. The connection must have survived the
			// cancelled wait; if pgx closed it, reconnect instead.
			if conn.Conn().PgConn().IsClosed() {
				return errors.New("db: notify hub connection closed by cancelled wait")
			}
			if err := h.syncListens(ctx, conn, listened); err != nil {
				return err
			}
			continue
		}
		return err
	}
}

// syncListens brings conn's LISTEN set in line with h.wanted and
// records the applied generation. listened is the connection's
// current set and is updated in place.
func (h *notifyHub) syncListens(ctx context.Context, conn *pgxpool.Conn, listened map[string]struct{}) error {
	h.mu.Lock()
	gen := h.gen
	want := make(map[string]struct{}, len(h.wanted))
	for c := range h.wanted {
		want[c] = struct{}{}
	}
	h.mu.Unlock()

	add, remove := listenDiff(listened, want)
	for _, c := range add {
		if _, err := conn.Exec(ctx, "LISTEN "+quoteIdent(c)); err != nil {
			return fmt.Errorf("db: notify hub LISTEN %s: %w", c, err)
		}
		listened[c] = struct{}{}
	}
	for _, c := range remove {
		if _, err := conn.Exec(ctx, "UNLISTEN "+quoteIdent(c)); err != nil {
			return fmt.Errorf("db: notify hub UNLISTEN %s: %w", c, err)
		}
		delete(listened, c)
	}

	h.mu.Lock()
	if gen > h.applied {
		h.applied = gen
	}
	h.lastErr = nil
	h.cond.Broadcast()
	h.mu.Unlock()
	return nil
}

// listenDiff returns the channels to LISTEN (in want, not in have) and
// to UNLISTEN (in have, not in want), each sorted for determinism.
func listenDiff(have, want map[string]struct{}) (add, remove []string) {
	for c := range want {
		if _, ok := have[c]; !ok {
			add = append(add, c)
		}
	}
	for c := range have {
		if _, ok := want[c]; !ok {
			remove = append(remove, c)
		}
	}
	sortStrings(add)
	sortStrings(remove)
	return add, remove
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// dispatch fans n out to every subscriber that wants its channel with
// a non-blocking send. Overflow drops the notification, counts it,
// and warns at most once a minute per subscriber.
func (h *notifyHub) dispatch(n Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !s.wants(n.Channel) {
			continue
		}
		select {
		case s.out <- n:
		default:
			s.dropped.Add(1)
			observeHub(func(o NotifyHubObserver) { o.HubDropped(n.Channel) })
			now := time.Now().UnixNano()
			if last := s.lastDrop.Load(); now-last >= int64(hubDropLogEvery) && s.lastDrop.CompareAndSwap(last, now) && h.log != nil {
				h.log.Warn("db: notify hub subscriber overflow; dropping notification (durable source + safety tick will recover)",
					"channel", n.Channel, "dropped_total", s.dropped.Load())
			}
		}
	}
}
