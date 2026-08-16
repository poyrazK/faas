// leader_url_publisher.go — Tier A9 / ADR-084 cache refresher.
//
// The writeGate (`pkg/gateway/writegate` +
// cmd/gatewayd-internal/write_gate.go) sits on every mutating
// request. It MUST NOT block on Postgres to ask "who is the
// leader?" — that's a synchronous round-trip on the customer
// hot path. Instead, the gate reads from a cached
// `writegate.CachedLeaderResolver`, and this publisher keeps
// the cache fresh.
//
// # Why a separate daemon-side goroutine
//
// The publisher subscribes to `compute_nodes_changed` via
// `pkg/db.SubscribeWithReconnect` (the same primitive
// `cmd/gatewayd-public/dns_handoff.go:50` uses for the
// Tier A8 active-passive handoff). Every notification
// deposits a non-blocking signal onto the resolver's
// refresh channel; the resolver's next `Current` call
// bypasses the TTL check and refreshes via singleflight.
//
// # Why this lives in cmd/gatewayd-internal (not in pkg/)
//
// The publisher is a daemon-side wiring concern — it owns
// the pgxpool, the slog logger, and the env-driven node
// name. A pure library wouldn't know about any of those.
// The pkg-level surface (the resolver, the signal channel,
// the singleflight coalescing) is fully unit-tested
// without the publisher; the publisher's only job is to
// translate pg_notify events into channel signals, which
// is verified by the integration test below.
//
// # Lifecycle
//
// Blocks until ctx is cancelled (daemon shutdown).
// On cancellation, returns nil — db.SubscribeWithReconnect
// closes its channel on ctx cancel (pkg/db/notify.go:316),
// so the inner `for { select { ... } }` exits cleanly.
package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

// runLeaderURLPublisher subscribes to compute_nodes_changed
// and pushes a signal onto `refresh` on every notification.
// The refresh channel is buffered to size 1 by the wiring
// code; if the resolver hasn't drained a previous signal
// yet, the new one collapses (the in-flight refresh will
// pick up the latest store state anyway).
//
// `nodeName` is captured at boot via FAAS_NODE_NAME and is
// used only for log fields. The resolver already knows its
// own node name (passed to NewCachedLeaderResolver at
// construction); the publisher doesn't echo it back into
// the resolver — the resolver's `isMe` decision is local
// to the cached snapshot.
//
// Returns nil on ctx cancellation. A pg_notify subscriber
// failure (initial subscribe error or fatal inner failure)
// returns a wrapped error so `run.go` can abort startup
// loudly — silently running without the publisher would
// mean the cache only refreshes on TTL, taking 5 s to
// propagate a leader flip (the operator-visible SLO is
// "failover visible within StandbyWriteLeaderURLCacheTTLSeconds
// = 5 s"; that's still met, but the §12 panel
// `gatewayd_internal_write_redirect_total{outcome="error"}`
// would be the only signal of the publisher outage, which
// is too quiet for an unrecoverable wiring miss).
func runLeaderURLPublisher(
	ctx context.Context,
	log *slog.Logger,
	pool *pgxpool.Pool,
	refresh chan<- struct{},
	nodeName string,
) error {
	if refresh == nil {
		return fmt.Errorf("gatewayd-internal: leader_url_publisher refresh channel is nil")
	}
	notif, err := db.SubscribeWithReconnect(ctx, pool, []string{
		db.NotifyComputeNodesChanged,
	}, log)
	if err != nil {
		return fmt.Errorf("gatewayd-internal: subscribe compute_nodes_changed: %w", err)
	}
	log.Info("gatewayd-internal: leader url publisher armed",
		"node", nodeName,
		"channel", db.NotifyComputeNodesChanged,
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				// db.SubscribeWithReconnect only closes
				// on ctx cancel (pkg/db/notify.go:316) —
				// this branch is the canonical shutdown
				// exit. Logged so a future refactor that
				// closes the channel on a transient
				// conn drop can be traced here.
				log.Info("gatewayd-internal: leader url publisher channel closed")
				return nil
			}
			log.Debug("gatewayd-internal: compute_nodes_changed received",
				"channel", n.Channel,
				"node", nodeName,
			)
			// Non-blocking send. If the resolver hasn't
			// drained the previous signal yet (it was
			// busy refreshing), this send collapses —
			// the in-flight refresh will read the LATEST
			// store state on completion, so we don't lose
			// information; we just coalesce redundant
			// work.
			select {
			case refresh <- struct{}{}:
			default:
			}
		}
	}
}
