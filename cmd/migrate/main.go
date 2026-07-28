// Command migrate — apply all pending migrations against $DATABASE_URL.
//
// Spec §5: schema lives in migrations/*.sql; goose-on-startup is the
// preferred path inside each daemon, but this binary exists so operators
// can apply migrations out-of-band (CI step, manual ops, pre-restart) and
// inspect the schema state.
//
// Usage:
//
//	DATABASE_URL=postgres://faas@/faas?host=/run/postgresql migrate
//	DATABASE_URL=... migrate -status    # report, apply nothing
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
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
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
		"pending_count", len(st.Pending),
		"pending", st.Pending,
	)
	if *statusOnly {
		return nil
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		return err
	}
	log.Info("migrations applied", "db_version", st.MaxEmbedded)
	return nil
}
