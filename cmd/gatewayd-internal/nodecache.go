// Issue #98 / ADR-028: gatewayd-internal's per-node vmmd client cache + the
// pg_notify subscriber that evicts entries when a row mutates.
//
// Production wiring (one NodeClientCache per gatewayd-internal process):
//
//	cache := gatewayd-internal.NewNodeClientCache(pgStore, vmmdTLS, log)
//	defer cache.Close()
//	go cache.WatchEvictions(ctx, pool)  // LISTEN compute_node_changed
//	handler.WithForwarding(gateway.ForwardingReverseProxy(cache, log))
//
// The cache is the gateway's hot path: every Wake→proxy roundtrip
// lands on it. It's safe for concurrent use; pkg/gateway/forwardproxy.go
// owns the per-call refcount so an in-flight ForwardHTTPStream RPC
// finishes against the conn it dialed before Evict closes the
// underlying transport. dialer errors fall through to 503 — the
// handler surfaces "node unavailable" and the client retries on
// the next hop.
//
// The resolver hook (gateway.SetNodeResolver) is set inside NewNodeClientCache
// once we know the pgStore; tests that don't care about the overlay
// path leave the package-level resolver as the zero value
// (returns "", false → ClientFor returns ok=false → 503).
//
// PR scale-out readiness: WatchEvictions wraps the existing subscribe
// loop with a heartbeat goroutine that bumps
// gateway_compute_node_changed_subscriber_alive every
// subscriberHeartbeatInterval (30s). The heartbeat ends on every
// WatchEvictions return path via defer so a ctx-cancel, channel close,
// OR the initial subscribe failure path leaves the gauge frozen at its
// last value — the freeze is the "I'm stale" signal the alert rule
// fires on (ops wiring is out of scope for this PR).

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
)

// subscriberHeartbeatInterval is the cadence at which the
// gateway_compute_node_changed_subscriber_alive gauge is bumped. 30s
// matches the per-process health-signal class (and the WakeGate ttl
// symmetry) — fast enough that an alert on "frozen > 2m" catches a
// real subscriber death without paging on a transient blip.
//
// PR scale-out readiness. This is a new constant in 4-PR #2.
const subscriberHeartbeatInterval = 30 * time.Second

// subscribeFunc is the test seam for WatchEvictions — production
// wires db.SubscribeWithReconnect; tests inject a fake channel so the
// heartbeat goroutine's lifetime can be driven synchronously.
//
// Mirrors the pattern in pkg/gateway/idle.go (idleticker_test.go uses
// a `now func() time.Time` field) and pkg/gateway/cert_expiry.go's
// `t *time.Ticker` parameterisation — the seam is local, the
// production default is right next to the declaration.
//
// PR scale-out readiness. New seam in 4-PR #2.
type subscribeFunc func(ctx context.Context, pool *pgxpool.Pool, channels []string, log *slog.Logger) (<-chan db.Notification, error)

// nodeCache bundles the gateway's per-node vmmd client cache with the
// subscribe-and-evict goroutine. Splitting it from cmd/gatewayd-internal/main.go
// keeps main.go focused on listener wiring; tests can construct the
// cache without booting a full daemon.
//
// PR scale-out readiness: the metrics field is the heartbeat's
// Prometheus gauge sink. Production wires deps.metrics (always
// non-nil after NewMetrics). Tests may pass nil; the heartbeat is
// nil-safe and a no-op in that case (see WatchEvictions).
//
// heartbeatStopped is a test-only exit signal — nil in production.
// When non-nil, the heartbeat goroutine calls it via defer on its
// own exit. Tests wire it to a sync.WaitGroup.Done so they can
// wait deterministically for the goroutine to exit (PR #407 review
// finding S2: replace time.Sleep with WaitGroup).
type nodeCache struct {
	cache   *gateway.NodeClientCache
	log     *slog.Logger
	metrics *gateway.Metrics
	// events is the optional pkg/events.Platform (issue #517 / PR-C /
	// ADR-064). When non-nil, the forwarder emits wake.proxy_first_byte
	// on the first downstream byte. nil opts out — pre-PR-C fixtures
	// + the legacy nodeCache wiring (deprecated; production always
	// passes a Platform).
	events *events.Platform
	// subscribe is the dependency seam described on subscribeFunc
	// above. Nil defaults to db.SubscribeWithReconnect in WatchEvictions.
	subscribe subscribeFunc
	// heartbeatStopped is described on the type doc above.
	heartbeatStopped func()
	// egressSink (issue #676 / ADR-080 PR-C) is the per-instance
	// egress ring (pkg/gateway/egresssink) that raw-stream
	// bytes flow into via ForwardingRawReverseProxyWithEvents's
	// RecordResponseBytes hook. nil opts out (legacy test
	// fixtures; pre-PR-C wiring); the sink's nil-safe guards
	// at the boundary make the call site a no-op. Production
	// wires the same *egresssink.EgressSink that run.go:1026
	// installs on the Handler via WithEgressSink — they share
	// the underlying ring buffer, so a raw-stream chunk written
	// by the forwarder and a plain HTTP chunk written by the
	// streaming wrap contribute to the same per-instance
	// usage_minutes.tx_bytes bucket.
	egressSink *egresssink.EgressSink
	// drain (issue #587 / PR-A) is the per-request WaitGroup-backed
	// drain tracker shared with the Handler / InternalReverseProxy /
	// TraceHandler. The raw-stream pump holds a Begin slot for the
	// lifetime of the hijacked conn so the graceful-shutdown drain
	// waits for in-flight raw pumps instead of force-closing the
	// conn on TimeoutStopSec=30s. nil = drain disabled (tests +
	// pre-PR-A behaviour).
	drain *drain.Tracker
}

// WithDrainTracker (issue #587 / PR-A) installs the per-request
// drain tracker the raw-stream factory captures in its closure.
// nil clears the tracker (returns the receiver for fluent
// chaining). Wired from cmd/gatewayd-internal/main.go alongside
// the Handler.WithInFlightTracker call so production has ONE
// tracker per daemon shared by every ServeHTTP surface.
func (n *nodeCache) WithDrainTracker(tracker *drain.Tracker) *nodeCache {
	n.drain = tracker
	return n
}

// WithEvents (issue #517 / PR-C / ADR-064) installs the events
// Platform that the forwarder will use to emit wake.proxy_first_byte.
// Returns the receiver so the call reads as a fluent setter at the
// wiring site in cmd/gatewayd-internal/main.go:
//
//	cache := newNodeCache(...).WithEvents(eventsPlatform)
//
// nil opts out (the legacy test fixtures + the WithEvents-less
// forwarder that the test suite still drives).
func (n *nodeCache) WithEvents(p *events.Platform) *nodeCache {
	n.events = p
	return n
}

// WithEgressSink (issue #676 / ADR-080 PR-C) installs the
// per-instance egress ring that raw-stream bytes flow into via
// rawStreamOnceWithEvents's RecordResponseBytes hook. Production
// passes the same *egresssink.EgressSink that run.go:1026 installs
// on the Handler via WithEgressSink — they share the underlying
// ring so a raw-stream chunk and a plain HTTP chunk contribute to
// the same usage_minutes.tx_bytes bucket. nil opts out (legacy
// fixtures). The wiring order in runWithDeps is
// WithEgressSink(egressSink) → deps.nodeCache.WithEgressSink(egressSink),
// keeping the second call's pointer identical so no copy of the
// ring buffer is needed.
func (n *nodeCache) WithEgressSink(sink *egresssink.EgressSink) *nodeCache {
	n.egressSink = sink
	return n
}

// newNodeCache wires the production cache: a *gateway.NodeClientCache
// whose dialer goes through pkg/wire.DialContext, with a resolver that
// asks pgStore for compute_nodes rows by id. The package-level
// gateway.SetNodeResolver is installed once at construction time so
// pkg/gateway doesn't have to import pkg/state (CLAUDE.md ownership).
//
// vmmdTLS may be nil for unix-only targets (single-box dev). tcp/dns
// targets must come with mTLS; the wire helper refuses a nil TLS on a
// non-unix scheme (issue #95).
//
// m may be nil in tests (WatchEvictions + TouchComputeNodeChangedSubscriber
// are nil-safe). Production passes deps.metrics which is always non-nil
// after NewMetrics.
//
// PR scale-out readiness: m parameter added in 4-PR #2.
func newNodeCache(store *state.PgStore, vmmdTLS *tls.Config, log *slog.Logger, m *gateway.Metrics) *nodeCache {
	if log == nil {
		log = slog.Default()
	}
	gateway.SetNodeResolver(func(ctx context.Context, nodeID string) (string, bool) {
		n, err := store.ComputeNodeByID(ctx, nodeID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return "", false
			}
			log.Warn("gateway: resolve compute_node for dial", "node", nodeID, "err", err.Error())
			return "", false
		}
		if !n.Active {
			return "", false
		}
		return n.TargetURL, true
	})
	cache := gateway.NewNodeClientCache(
		func(ctx context.Context, target string) (*grpc.ClientConn, error) {
			// Issue #120: route through pkg/overlay so the cross-box
			// dial primitive lives in one place. The cache above is
			// unrelated; only the underlying wire dial is swapped.
			return overlay.Dial(ctx, overlay.New(target), vmmdTLS)
		},
		log,
	)
	return &nodeCache{cache: cache, log: log, metrics: m, subscribe: db.SubscribeWithReconnect}
}

// Forwarding returns the per-node http.Handler factory. cmd/gatewayd-internal
// installs it on the gateway.Handler via WithForwarding so every
// request dispatches through the cache.
//
// PR-C (issue #517 / ADR-064): when n.events is non-nil the
// forwarder emits wake.proxy_first_byte on the first downstream
// byte. nil opts out.
func (n *nodeCache) Forwarding() func(gateway.Target) http.Handler {
	return gateway.ForwardingReverseProxyWithEvents(n.cache, n.log, n.events)
}

// RawForwarding (issue #676 / ADR-080) is the raw-bytes Upgrade
// bridge counterpart of Forwarding. Both factories share the same
// underlying NodeClientCache so the per-node gRPC channel is
// reused regardless of which RPC is in flight (only the RPC
// method differs: ForwardHTTPStream vs ForwardRawStream). The
// handler's three-input gate routes Connection: Upgrade requests
// here BEFORE falling through to Forwarding.
func (n *nodeCache) RawForwarding() func(gateway.Target) http.Handler {
	return gateway.ForwardingRawReverseProxyWithEventsAndDrain(n.cache, n.log, n.events, n.egressSink, n.drain)
}

// Close shuts down every cached *grpc.ClientConn. Called once at
// shutdown; subsequent ClientFor calls return ok=false so any
// in-flight listener draining sees "node unavailable" → 503.
func (n *nodeCache) Close() error { return n.cache.Close() }

// WatchEvictions subscribes to the compute_node_changed pg_notify
// channel and calls cache.Evict(nodeID) for each notification. Runs
// until ctx is cancelled. Uses db.SubscribeWithReconnect so a Postgres
// restart doesn't strand the cache in a stale state (the parallel to
// watchInvalidations on the routing side).
//
// PR scale-out readiness (4-PR #2): also runs a heartbeat goroutine
// that bumps gateway_compute_node_changed_subscriber_alive every
// subscriberHeartbeatInterval (30s) while the subscribe loop is
// alive. Mirrors the StartCertExpiryRefresher pattern
// (pkg/gateway/cert_expiry.go:61-101): a `done` channel + ticker,
// stop() closure idempotent + non-blocking, first tick delayed by one
// interval (no spurious bump at boot).
//
// On ctx cancel, channel close, OR the initial subscribe failure,
// the heartbeat goroutine ends (via defer stop). The gauge freezes at
// its last value — the freeze is the "I'm stale" signal the alert
// rule fires on. Heartbeat is nil-safe when n.metrics is nil (tests
// pass nil *Metrics) — the goroutine still runs and respects ctx, it
// just doesn't emit a metric.
//
// interval=0 uses the package-level subscriberHeartbeatInterval
// (production default = 30s). Tests pass a short interval (e.g. 50ms)
// to drive the heartbeat synchronously.
func (n *nodeCache) WatchEvictions(ctx context.Context, pool *pgxpool.Pool) {
	n.watchEvictionsWithInterval(ctx, pool, subscriberHeartbeatInterval)
}

// watchEvictionsWithInterval is the test seam for the heartbeat
// cadence. Production callers use WatchEvictions (zero work).
// interval <= 0 is treated as subscriberHeartbeatInterval so a
// misconfigured caller cannot silently disable the heartbeat.
//
// Unexported so the seam doesn't widen the production API surface
// (PR #407 review finding S3). Tests in nodecache_test.go are
// package-internal and reach the seam directly.
//
// PR scale-out readiness (4-PR #2).
func (n *nodeCache) watchEvictionsWithInterval(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) {
	if interval <= 0 {
		interval = subscriberHeartbeatInterval
	}

	subscribe := n.subscribe
	if subscribe == nil {
		subscribe = db.SubscribeWithReconnect
	}

	// defer-stop-before-start: the stop closure is a no-op until the
	// heartbeat goroutine is launched below. Mirrors the same pattern
	// in StartCertExpiryRefresher (pkg/gateway/cert_expiry.go:90-99).
	heartbeatDone := make(chan struct{})
	defer func() {
		select {
		case <-heartbeatDone:
			// Already closed (goroutine already exited).
		default:
			close(heartbeatDone)
		}
	}()

	notif, err := subscribe(ctx, pool, []string{db.NotifyComputeNodesChanged}, n.log)
	if err != nil {
		// First-subscribe failure: do NOT start the heartbeat. The
		// gauge stays at 0; the alert rule fires. Production's
		// ctx.Done probe in cmd/gatewayd-internal/main.go's SIGHUP path
		// handles daemon shutdown — we don't need to block here.
		n.log.Error("gatewayd-internal: subscribe compute_nodes_changed", "err", err)
		return
	}

	// Heartbeat runs from after the first tick onward; the first tick
	// fires after one interval, not immediately (no spurious bump at
	// subscriber boot, same as StartCertExpiryRefresher). Not started
	// when n.metrics is nil — saves a goroutine for nil-metrics tests.
	if n.metrics != nil {
		go func() {
			defer func() {
				if n.heartbeatStopped != nil {
					n.heartbeatStopped()
				}
			}()
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-heartbeatDone:
					return
				case <-t.C:
					n.metrics.TouchComputeNodeChangedSubscriber()
				}
			}
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case got, ok := <-notif:
			if !ok {
				return
			}
			var p struct {
				NodeID string `json:"node_id"`
				Active bool   `json:"active"`
			}
			if err := json.Unmarshal([]byte(got.Payload), &p); err != nil || p.NodeID == "" {
				n.log.Warn("gatewayd-internal: bad compute_node_changed payload", "payload", got.Payload)
				continue
			}
			// Evict on every mutation: the resolver re-reads the row
			// (active / target_url / overlay_ip) on the next ClientFor,
			// so a re-activation transparently re-dials with the fresh
			// state. We don't try to be clever and selectively evict
			// only on active=false — the dial cost on a Tailscale
			// overlay is sub-100 ms and the simpler invariant is worth
			// the occasional extra dial.
			n.cache.Evict(p.NodeID)
			n.log.Info("gatewayd-internal: evicted node client cache",
				"node", p.NodeID, "active", p.Active)
		}
	}
}
