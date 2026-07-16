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
package main

import (
	"context"
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
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx := context.Background()

	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		return err
	}
	log.Info("migrations applied")
	return nil
}
