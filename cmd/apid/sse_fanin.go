// sseFanIn bridges cross-process pg_notify deliveries to the in-process
// events.Broadcaster (Move 3, M7.5 prep).
//
// Why this exists: pkg/db/notify.go::SubscribeWithReconnect gives apid a
// reconnecting LISTEN session that survives Postgres restarts; the
// dashboard's /v1/events handler subscribes per-request via the Notifier
// (each browser EventSource owns its own dedicated connection). But the
// in-process events.Broadcaster has no producer — only one consumer
// (streamDeploymentLogs on TopicDeploymentLog), and no one publishes.
//
// This goroutine is the single producer. It owns one reconnecting LISTEN
// across the eight SSE-relevant channels and republishes each notification
// into the broadcaster under the same channel name. The dashboard
// handler keeps using its own per-request subscription for
// account-scoping; the broadcaster is the route any in-process handler
// that wants "I just got told X" takes.
//
// The "first frame wins on (invocation_id, state)" contract for
// invocation_done consumers is documented in handlers_events.go next to
// the eventsChannels declaration; this fan-in does NOT dedupe — the DB
// trigger and the drain both fire and dedup is the consumer's job.
package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
)

// sseChannels is the set of pg_notify channels the fan-in republishes.
// Mirrors cmd/apid/handlers_events.go::eventsChannels plus
// NotifyInvocationDone (added in Move 3 so the dashboard reacts when an
// async invoke / queue / delayed-task / cron row lands) and
// NotifyDebugRegressionChanged (live debugger lifecycle updates). One set, kept
// in sync between the per-request subscriber and the fan-in: the
// fan-in's job is to make the broadcaster a faithful mirror of
// pg_notify for in-process consumers; the per-request subscriber's
// job is account-scope filtering, which is unrelated.
var sseChannels = []string{
	db.NotifyAppChanged,
	db.NotifyDeploymentChanged,
	db.NotifyInstanceChanged,
	db.NotifyCronFired,
	db.NotifyQuotaWarning,
	db.NotifyBillingPastDue,
	db.NotifyInvocationDone,
	db.NotifyDebugRegressionChanged,
	// Wave 0 PR-C / ADR-047: stateless-advisory fan-in mirror.
	// MUST stay in lock-step with cmd/apid/handlers_events.go::
	// eventsChannels — see the comment there.
	db.NotifyStatelessAdvisory,
}

// sseSubscribeFn is the subscription seam sseFanIn depends on.
// Production calls db.SubscribeWithReconnect (the helper below passes
// the captured pool through); tests inject a fake that returns a
// channel of synthetic notifications so the bridge can be exercised
// without a live Postgres.
type sseSubscribeFn func(ctx context.Context, log *slog.Logger) (<-chan db.Notification, error)

// sseFanIn blocks until ctx is cancelled. It opens one reconnecting
// LISTEN session on `channels` and forwards every notification to the
// in-process broadcaster under the same channel name.
//
// If `subscribe` is nil, the production subscribe (db.SubscribeWithReconnect
// against `pool`) is used. The production wiring in run() passes
// `pool`; tests pass a fake subscribe and a nil pool.
//
// nil broadcaster returns immediately — the broadcaster is required
// for any meaningful work. Returning early is defensive; the
// constructor in newServerWithDeps guarantees a non-nil one in
// production and tests.
func sseFanIn(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, bc *events.Broadcaster, subscribe sseSubscribeFn) {
	if bc == nil {
		if log != nil {
			log.Error("sse fan-in: nil broadcaster; not starting")
		}
		return
	}
	if subscribe == nil {
		if pool == nil {
			if log != nil {
				log.Error("sse fan-in: nil pool and no subscribe seam; not starting")
			}
			return
		}
		subscribe = func(ctx context.Context, log *slog.Logger) (<-chan db.Notification, error) {
			return db.SubscribeWithReconnect(ctx, pool, sseChannels, log)
		}
	}
	notif, err := subscribe(ctx, log)
	if err != nil {
		if log != nil {
			log.Error("sse fan-in: initial Subscribe failed; bridge offline", "err", err)
		}
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-notif:
			if !ok {
				// SubscribeWithReconnect's outer channel only closes on
				// ctx cancel; if we land here the wrapper contract is
				// broken — log + exit so a future ping picks up the gap.
				if log != nil {
					log.Warn("sse fan-in: notification channel closed unexpectedly")
				}
				return
			}
			// Fan-in does NOT dedupe. Both the DB trigger at
			// migrations/00031_invocations_notify.sql and the
			// schedd drain emitDone publish invocation_done on the
			// same transition; the per-request SSE handler in
			// handlers_events.go sees each notification exactly once
			// (it owns its own LISTEN session, not this shared one)
			// and the dashboard's htmx-sse consumer is naturally
			// idempotent (fragment re-render). The CLI's faas tail
			// prints the same id+state line twice without harm —
			// the dedup contract lives at the consumer, not here.
			//
			// PublishTopic is non-blocking with per-subscriber
			// buffers (pkg/events/broadcaster.go). The dashboard
			// handler doesn't read TopicDeploymentLog from the
			// broadcaster, so a busy build log stream drops on the
			// (unread) buffer rather than back-pressuring pg_notify.
			bc.PublishTopic(n.Channel, []byte(n.Payload))
		}
	}
}
