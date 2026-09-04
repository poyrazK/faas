// Command migrate — apply all pending migrations against $DATABASE_URL.
//
// Spec §5: schema lives in migrations/*.sql; goose-on-startup is the
// preferred path inside each daemon, but this binary exists so operators
// can apply migrations out-of-band (CI step, manual ops, pre-restart) and
// inspect the schema state.
//
// Modes (PR-2 / audit F2-B / ADR-124 amendment):
//
//	default            — apply every pending migration synchronously,
//	                     then exit. Backwards-compatible behaviour;
//	                     safe for single-box installs and CI steps.
//
//	-leader            — same as default, but log that this process is
//	                     the cluster's migration leader. Used at the
//	                     head of a fleet bootstrap so the other daemons
//	                     (running with -wait-for-migrations) block on
//	                     pg_notify('migrations_applied') before opening
//	                     their own connections. The advisory lock from
//	                     PR-1 still wraps MigrateUp as the safety net
//	                     for parallel boot without a leader.
//
//	-wait-for-migrations — do NOT apply. Block until the leader has
//	                     applied every embedded migration ID.
//	                     Subscribes to db.NotifyMigrationsApplied;
//	                     polls the ledger every waitPollInterval as a
//	                     safety net for notify loss. The non-leader
//	                     box's main() invokes this BEFORE its own
//	                     db.MigrateUp.
//
// Usage:
//
//	DATABASE_URL=postgres://faas@/faas?host=/run/postgresql migrate
//	DATABASE_URL=... migrate -status                 # report, apply nothing
//	DATABASE_URL=... migrate -leader                 # fleet bootstrap
//	DATABASE_URL=... migrate -wait-for-migrations    # non-leader boxes
//	DATABASE_URL=... migrate -leader -status         # report + log "leader"
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/onebox-faas/faas/pkg/db"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// -status reports what `migrate` WOULD apply and exits without applying.
	// The deploy runs it before the real apply so the CI log always records
	// the box's before-state; when a migration then fails, "was the box where
	// we thought it was?" is answerable from the log instead of an SSH
	// session.
	statusOnly := flag.Bool("status", false,
		"report current DB version and pending migrations, then exit without applying")
	// -leader marks this run as the cluster's migration leader. The
	// behaviour is identical to the default mode (apply every pending
	// migration synchronously); the flag exists so the operator's
	// intent is captured in the log and in any monitoring that
	// greps for "mode=leader". A leader IS NOT REQUIRED in a
	// single-box install; the advisory lock from PR-1 alone is
	// sufficient. -leader is the preferred multi-host boot order.
	leader := flag.Bool("leader", false,
		"apply pending migrations as the cluster's leader; non-leader daemons should pass -wait-for-migrations")
	// -wait-for-migrations blocks until the leader has applied every
	// embedded migration, then exits. Mutually exclusive with -status
	// (a CI -status run should not block). Mutually exclusive with
	// -leader (one process is either a leader or a waiter, never both).
	waitForMigrations := flag.Bool("wait-for-migrations", false,
		"block until the cluster's migration leader has applied every embedded migration, then exit (does not apply)")
	flag.Parse()

	if *leader && *waitForMigrations {
		return fmt.Errorf("migrate: -leader and -wait-for-migrations are mutually exclusive")
	}
	if *waitForMigrations && *statusOnly {
		return fmt.Errorf("migrate: -wait-for-migrations and -status are mutually exclusive")
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	mode := "apply"
	if *leader {
		mode = "leader"
	}
	if *waitForMigrations {
		mode = "wait-for-migrations"
	}
	log = log.With("mode", mode)

	ctx := context.Background()

	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer pool.Close()

	st, err := db.Status(ctx, pool)
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	log.Info("migration status",
		"db_version", st.DBVersion,
		"max_embedded", st.MaxEmbedded,
		"embedded_count", len(st.EmbeddedVersions),
		"pending_count", len(st.Pending),
		"pending", st.Pending,
	)
	if *statusOnly {
		return nil
	}

	// Waiter: block on the leader's last migration, then exit.
	// The waiter never calls MigrateUp; if the leader is still
	// running, the advisory lock from PR-1 would serialize the
	// waiter's eventual MigrateUp behind the leader, but the
	// waiter never reaches that path — it exits as soon as the
	// ledger is current. The daemon's own MigrateUp runs after
	// this helper returns, gated by the advisory lock if another
	// box is somehow mid-migration.
	if *waitForMigrations {
		log.Info("migrate: blocking on leader")
		if err := WaitForMigrationsApplied(ctx, pool, st.EmbeddedVersions, log); err != nil {
			return fmt.Errorf("wait-for-migrations: %w", err)
		}
		log.Info("migrate: leader caught up; exiting")
		return nil
	}

	// Leader / default: apply everything synchronously. The
	// advisory lock from PR-1 wraps MigrateUp; if a second box
	// (somehow) races us with -leader and a parallel default
	// run, the second one blocks until we finish.
	if err := db.MigrateUp(ctx, pool); err != nil {
		return err
	}
	after, err := db.Status(ctx, pool)
	if err != nil {
		return fmt.Errorf("post-migrate status: %w", err)
	}
	if len(after.Pending) != 0 {
		return fmt.Errorf("post-migrate verification: %d embedded migrations remain pending: %v", len(after.Pending), after.Pending)
	}
	log.Info("migrations applied",
		"db_version", after.DBVersion,
		"embedded_count", len(after.EmbeddedVersions))
	return nil
}
