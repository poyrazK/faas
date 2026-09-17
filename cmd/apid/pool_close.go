package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// closePoolAfterCancel cancels the run context, then closes the pool.
//
// Order is the whole point. pgxpool.Close blocks until every acquired
// connection has been returned, and apid's LISTEN subscribers (openapi_doc,
// audit, deploy_failed_email, …) each hold one for as long as their context
// lives. Closing the pool first therefore waited on goroutines that were
// waiting on a cancel that only SIGTERM would deliver: any startup error
// after the subscribers had started hung apid inside its own deferred
// cleanup — pool marked closed (so the audit outbox logged "closed pool"
// every two seconds against a still-live context), readiness drained, the
// listener never served, the error never returned and never logged. On the
// acceptance node that read as
//
//	e2etest: apid still running, output: …
//	e2etest: 127.0.0.1:37489 did not accept within 30s
//
// three times in one smoke run (35212091363) and at least twice before,
// each time 170ms after boot with no reason on the host.
//
// Cancelling first lets every context-bound holder release, so Close
// completes, the error reaches wire.Daemon, and the process exits with the
// reason in its log.
func closePoolAfterCancel(cancel context.CancelFunc, pool *pgxpool.Pool) {
	if cancel != nil {
		cancel()
	}
	if pool != nil {
		pool.Close()
	}
}
