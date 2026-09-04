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
// enough that the steady-state cost is one indexed ledger-set query per
// daemon per interval.
//
// The cost is per-daemon and constant; at fleet sizes we plan to run
// (single-digit control-plane boxes), that's negligible. If the
// fleet ever grows past a few dozen boxes the same primitive can be
// tuned via an env var; not worth the surface area today.
const waitPollInterval = 5 * time.Second

// WaitForMigrationsApplied blocks until either:
//   - the leader has applied every embedded migration ID, or
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
//     required migration row; waiter subscribed first sees the notify.
//   - TestWaitForMigrationsApplied_NoOpIfAlreadyCurrent — waiter's
//     initial ledger contains the complete required set; returns
//     immediately, never subscribes.
//   - TestWaitForMigrationsApplied_RespectsContextCancel — caller
//     cancels mid-wait; helper returns ctx.Err() inside the deadline.
func WaitForMigrationsApplied(
	ctx context.Context,
	pool *pgxpool.Pool,
	expectedVersions []int64,
	log *slog.Logger,
) error {
	if pool == nil {
		return fmt.Errorf("migrate: WaitForMigrationsApplied: nil pool")
	}
	if len(expectedVersions) == 0 {
		// An empty set means the binary ships no migrations
		// (e.g. a development build before any DDL landed). Nothing
		// to wait for; return success. This is distinct from
		// []int64{1}, where exactly one migration exists and we
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
	missing, err := readMissingAppliedMigrations(ctx, pool, expectedVersions)
	if err != nil {
		return fmt.Errorf("migrate: WaitForMigrationsApplied initial check: %w", err)
	}
	if len(missing) == 0 {
		log.Info("migrate: already current", "required_count", len(expectedVersions))
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
		"applied_count", len(expectedVersions)-len(missing),
		"required_count", len(expectedVersions),
		"missing_count", len(missing),
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
			// information over a direct ledger-set query.
			missing, err := readMissingAppliedMigrations(ctx, pool, expectedVersions)
			if err != nil {
				return fmt.Errorf("migrate: WaitForMigrationsApplied recheck: %w", err)
			}
			if len(missing) == 0 {
				log.Info("migrate: caught up via notify",
					"required_count", len(expectedVersions))
				return nil
			}

		case <-ticker.C:
			missing, err := readMissingAppliedMigrations(ctx, pool, expectedVersions)
			if err != nil {
				// Don't bail on a transient blip — the next tick
				// or the next notify will re-check. Log and
				// continue.
				log.Warn("migrate: WaitForMigrationsApplied poll error",
					"err", err)
				continue
			}
			if len(missing) == 0 {
				log.Info("migrate: caught up via poll",
					"required_count", len(expectedVersions))
				return nil
			}
		}
	}
}

// readMissingAppliedMigrations compares the binary's complete required set
// with the applied ledger. Comparing maxima is insufficient after ADR-142:
// migration 12:00 may merge after migration 15:00 and still need applying.
func readMissingAppliedMigrations(ctx context.Context, pool *pgxpool.Pool, expected []int64) ([]int64, error) {
	rows, err := pool.Query(ctx,
		`SELECT version_id
		   FROM (
		       SELECT DISTINCT ON (version_id) version_id, is_applied
		         FROM goose_db_version
		        WHERE version_id = ANY($1::bigint[])
		        ORDER BY version_id, id DESC
		   ) AS latest
		  WHERE is_applied = true`, expected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int64]struct{}, len(expected))
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	missing := make([]int64, 0)
	for _, version := range expected {
		if _, ok := applied[version]; !ok {
			missing = append(missing, version)
		}
	}
	return missing, nil
}
