// pgratelimit_invalidator.go contains the retired LISTEN-side consumer for the
// former per-process admission cache. Central mode now consumes the shared row
// on every request and migration 20260917160000002 removes the per-consume
// NOTIFY trigger. The type remains available for rolling-version compatibility
// and focused tests, but production no longer starts it.
//
// The Drain-loop shape mirrors pkg/wire/pgverifier.go
// (PGNodeVerifier, ADR-056): subscribeWithReconnect +
// Run(ctx, ch). nil-receiver is tolerated (no-op drain; the
// daemon simply doesn't wire the invalidator when [ratelimit]
// mode = "local" — the default).

package wire

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

// RateLimitInvalidatorSink is the callback shape the
// production Handler wires. For each notification payload the
// Run loop decodes the (scope, subject_id, plan) triple and
// passes it to Invalidate — the Handler implements the
// sink by walking each of its per-process Limiters and
// dropping the matching bucket.
//
// The seam mirrors NodeVerifier's NodeLoader pattern
// (pkg/wire/pgverifier.go:60-65): a small interface lets tests
// swap an in-memory sink without standing up a real pool.
type RateLimitInvalidatorSink interface {
	InvalidateRateLimit(scope, subjectID, plan string)
}

// PGRateLimitInvalidator is the production-side invalidator.
// Holds the pgxpool + sink; Run drains the channel until ctx
// is cancelled. Constructed iff [ratelimit] mode = "central"
// (cmd/gatewayd-internal/run.go's buildCentralRateLimitBackend
// call site).
type PGRateLimitInvalidator struct {
	pool *pgxpool.Pool
	sink RateLimitInvalidatorSink
	log  *slog.Logger
}

// NewPGRateLimitInvalidator constructs the invalidator. pool
// is shared with the daemon's main store (no second pool); sink
// is the Handler's WithCentralBackend-injected invalidation
// surface. log is required (pass slog.Default() in tests that
// don't care).
func NewPGRateLimitInvalidator(pool *pgxpool.Pool, sink RateLimitInvalidatorSink, log *slog.Logger) *PGRateLimitInvalidator {
	if log == nil {
		log = slog.Default()
	}
	return &PGRateLimitInvalidator{pool: pool, sink: sink, log: log}
}

// Run drains the 'rate_limit_changed' channel until ctx is
// cancelled. Each notification payload is JSON-decoded into a
// RateLimitChangedPayload; the (scope, subject_id, plan) triple
// is forwarded to the sink via InvalidateRateLimit. Decode
// failures are logged + skipped — the daemon must not crash on
// a malformed payload (any daemon with pg_notify write access
// can craft adversarial strings).
//
// Backoff / reconnect is handled by the underlying
// db.SubscribeWithReconnect wrapper — the drain loop is
// fire-and-forget over the returned channel.
func (i *PGRateLimitInvalidator) Run(ctx context.Context) error {
	if i.pool == nil {
		return fmt.Errorf("pgratelimit invalidator: nil pool")
	}
	if i.sink == nil {
		return fmt.Errorf("pgratelimit invalidator: nil sink")
	}
	ch, err := db.SubscribeWithReconnect(ctx, i.pool, []string{db.NotifyRateLimitChanged}, i.log)
	if err != nil {
		return fmt.Errorf("pgratelimit invalidator: subscribe: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				// Channel closed (sentinel — SubscribeWithReconnect
				// only closes the channel on ctx cancel). Loop
				// exits via the ctx.Done() arm on the next tick.
				return nil
			}
			var p RateLimitChangedPayload
			if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
				i.log.Warn("pgratelimit invalidator: malformed payload",
					"err", err, "payload", n.Payload)
				continue
			}
			i.sink.InvalidateRateLimit(p.Scope, p.SubjectID, p.Plan)
		}
	}
}

// RateLimitChangedPayload is the JSON wire shape emitted by
// the pg_ratelimit_counters_notify trigger
// (migrations/00126_pg_ratelimit.sql). Decoded into the three
// fields the sink cares about. Field tags MUST stay in sync
// with the SQL json_build_object call.
type RateLimitChangedPayload struct {
	Scope     string `json:"scope"`
	SubjectID string `json:"subject_id"`
	Plan      string `json:"plan"`
}
