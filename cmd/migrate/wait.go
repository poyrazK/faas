package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

// waitPollInterval is the periodic refresh cadence used by
// WaitForMigrationsApplied between notification deliveries. The
// subscriber's primary wake signal is pg_notify('migrations_applied');
// this ticker is the safety net for the notify-loss case
// (Postgres restart, transport hiccup between INSERT and the LISTEN
// session). Five seconds is short enough that an operator watching
// `migrate -wait-for-migrations` sees progress in human time, long
// enough that the steady-state cost is one cheap MAX(version_id)
// query per daemon per interval.
//
// The cost is per-daemon and constant; at fleet sizes we plan to run
// (single-digit control-plane boxes), that's negligible. If the
// fleet ever grows past a few dozen boxes the same primitive can be
// tuned via an env var; not worth the surface area today.
const waitPollInterval = 5 * time.Second

// WaitForMigrationsApplied blocks until either:
//   - the leader has applied every embedded migration (the
//     goose_db_version ledger's MAX(version_id) >= maxEmbedded), or
//   - ctx is cancelled (caller-driven shutdown / CLI Ctrl-C).
//
// The non-leader path of `cmd/migrate -wait-for-migrations`
// (`run` below) calls this before its own MigrateUp. The leader
// does NOT call this; it runs the migration directly and lets the
// trigger on goose_db_version fan the signal out to waiters.
//
// Why a leader/waiter split in addition to the pg_advisory_lock
// from PR-1: the lock prevents concurrent migrations, not
// out-of-order migrations. If box A is the leader and box B boots
// 10 seconds later, B's MigrateUp would acquire the lock the
// instant A releases it — but A hasn't even started yet, so B
// blocks on the lock until A finishes the whole UpContext. The
// leader/waiter pattern makes box B's clock-driven boot order
// deterministic: B sees the leader's commit before B opens its own
// session, no lock contention, no surprises.
//
// Failure modes:
//   - ctx cancel         → returns ctx.Err(); the daemon exits cleanly.
//   - Leader crash / never-runs → waiters block forever (operator-
//     visible; the helper does NOT time out silently). Operators
//     run `migrate -leader` first; missing leader = operator error.
//   - Postgres restart   → waitPollInterval picks up the catch-up
//     on the next tick even if the notify is lost.
//
// Tests:
//   - TestWaitForMigrationsApplied_NotifyUnblocks — leader inserts a
//     row at MAX(version_id); waiter subscribed first sees the notify.
//   - TestWaitForMigrationsApplied_NoOpIfAlreadyCurrent — waiter's
//     initial MAX(version_id) already >= maxEmbedded; returns
//     immediately, never subscribes.
//   - TestWaitForMigrationsApplied_RespectsContextCancel — caller
//     cancels mid-wait; helper returns ctx.Err() inside the deadline.
func WaitForMigrationsApplied(
	ctx context.Context,
	pool *pgxpool.Pool,
	maxEmbedded int64,
	log *slog.Logger,
) error {
	if pool == nil {
		return fmt.Errorf("migrate: WaitForMigrationsApplied: nil pool")
	}
	if maxEmbedded <= 0 {
		// maxEmbedded==0 means the binary ships no migrations
		// (e.g. a development build before any DDL landed). Nothing
		// to wait for; return success. This is distinct from
		// maxEmbedded==1, where exactly one migration exists and we
		// must confirm it has been applied.
		return nil
	}
	if log == nil {
		log = slog.Default()
	}

	// Fast path: the leader has already finished before we even
	// got here. Common in dev loops where the operator reruns
	// `migrate -wait-for-migrations` after the leader crashed and
	// was restarted manually; the ledger is already at the head.
	current, err := readMaxApplied(ctx, pool)
	if err != nil {
		return fmt.Errorf("migrate: WaitForMigrationsApplied initial check: %w", err)
	}
	if current >= maxEmbedded {
		log.Info("migrate: already current", "db_version", current, "max_embedded", maxEmbedded)
		return nil
	}

	// Subscribe BEFORE entering the polling tick so the leader's
	// commit between the SELECT above and the LISTEN below is not
	// lost. SubscribeWithReconnect handles connection drops and
	// LISTEN errors transparently; the daemon sees one stable
	// channel arm forever.
	notifs, err := db.SubscribeWithReconnect(ctx, pool,
		[]string{db.NotifyMigrationsApplied}, log)
	if err != nil {
		return fmt.Errorf("migrate: WaitForMigrationsApplied LISTEN: %w", err)
	}

	ticker := time.NewTicker(waitPollInterval)
	defer ticker.Stop()

	log.Info("migrate: waiting for leader",
		"current", current, "max_embedded", maxEmbedded,
		"channel", db.NotifyMigrationsApplied)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case _, ok := <-notifs:
			if !ok {
				// SubscribeWithReconnect only closes the outer
				// channel on ctx.Done; if we see !ok without a
				// cancel, the pool is gone or the inner loop is
				// wedged. Treat as a hard failure — the operator
				// must restart the daemon and re-run the leader.
				return fmt.Errorf("migrate: WaitForMigrationsApplied: LISTEN channel closed unexpectedly")
			}
			// Re-read the ledger. The trigger payload is the
			// version_id as decimal text; parsing it adds no
			// information over a direct MAX() query.
			cur, err := readMaxApplied(ctx, pool)
			if err != nil {
				return fmt.Errorf("migrate: WaitForMigrationsApplied recheck: %w", err)
			}
			if cur >= maxEmbedded {
				log.Info("migrate: caught up via notify",
					"db_version", cur, "max_embedded", maxEmbedded)
				return nil
			}

		case <-ticker.C:
			cur, err := readMaxApplied(ctx, pool)
			if err != nil {
				// Don't bail on a transient blip — the next tick
				// or the next notify will re-check. Log and
				// continue.
				log.Warn("migrate: WaitForMigrationsApplied poll error",
					"err", err)
				continue
			}
			if cur >= maxEmbedded {
				log.Info("migrate: caught up via poll",
					"db_version", cur, "max_embedded", maxEmbedded)
				return nil
			}
		}
	}
}

// readMaxApplied returns MAX(version_id) of is_applied=true rows in
// goose_db_version, or 0 if the ledger is empty (fresh DB). The
// empty-ledger case is treated as "needs migration"; the leader
// will insert the first row and the trigger will notify.
func readMaxApplied(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var v int64
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = true`,
	).Scan(&v)
	if err != nil {
		return 0, err
	}
	return v, nil
}
