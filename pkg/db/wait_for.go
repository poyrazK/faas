package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WaitForNotification subscribes to `channel` for the lifetime of `ctx`
// and returns the first notification payload for which `predicate`
// returns true, or the error from `ctx` / timeout. The dedicated
// connection is released on exit.
//
// Use for ad-hoc long-polls gated by payload content (e.g. queueReceive
// scoping to its app_id, invokeApp waiting on its own invocation id).
// The drain and daemon-side loops use SubscribeWithReconnect because they
// own a long-lived LISTEN session across many event types; this helper
// is the per-request equivalent.
//
// The timeout is the server-side cap on the long-poll. Callers MUST
// bound it; the helper does not impose a default so a buggy caller
// cannot hang a connection past the apid request's read timeout.
//
// Behaviour:
//   - Open a fresh pool.Acquire per call (no shared LISTEN state, so
//     each WaitFor carries its own race window — the daemon's queue
//     keeps draining while the long-poll sits on its dedicated conn).
//   - LISTEN channel on the dedicated connection.
//   - WaitForNotification(ctx) in a loop; on each non-error delivery,
//     run predicate(payload); on true, return the payload.
//   - On ctx.Done() or timeout: return ctx.Err() / ErrWaitTimeout.
//   - Release the connection on every exit path (defer).
//
// Payload security: the predicate is a plain function over the raw
// payload string. Callers that compare against an ID MUST treat the
// payload as adversarial input (any daemons with pg_notify write access
// can craft strings). The convention in this codebase is JSON; a
// Contains(p, id) check is preferable to a JSON decode here because
// decode forces a single-valued allocation on every notify.
func WaitForNotification(
	ctx context.Context, pool *pgxpool.Pool,
	channel string, predicate func(payload string) bool,
	timeout time.Duration,
) (string, error) {
	if pool == nil {
		return "", fmt.Errorf("db: WaitForNotification: nil pool")
	}
	if channel == "" {
		return "", fmt.Errorf("db: WaitForNotification: empty channel")
	}
	if predicate == nil {
		return "", fmt.Errorf("db: WaitForNotification: nil predicate")
	}
	if timeout <= 0 {
		return "", ErrWaitTimeout
	}

	// Bound the parent ctx by the timeout so the dedicated connection
	// is released on the timeout path even when the caller forgets to
	// cancel. The caller's ctx is preserved so a client cancel still
	// returns ctx.Err().
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Session-scoped: see direct.go.
	conn, err := DirectPool(pool).Acquire(waitCtx)
	if err != nil {
		return "", fmt.Errorf("db: acquire listener: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(waitCtx, fmt.Sprintf("LISTEN %s", quoteIdent(channel))); err != nil {
		return "", fmt.Errorf("db: LISTEN %s: %w", channel, err)
	}

	for {
		n, err := conn.Conn().WaitForNotification(waitCtx)
		if err != nil {
			// timeout / ctx-cancel both surface here. We want callers
			// to see ErrWaitTimeout when the timeout fired (queueReceive
			// maps that to 204 No Content) and ctx.Err() when the
			// client cancelled.
			if waitCtx.Err() == context.DeadlineExceeded {
				return "", ErrWaitTimeout
			}
			return "", err
		}
		if predicate(n.Payload) {
			return n.Payload, nil
		}
		// Non-matching payload: keep waiting. We don't release the
		// connection on miss — the LISTEN session is the only thing
		// keeping the predicate check live.
	}
}
