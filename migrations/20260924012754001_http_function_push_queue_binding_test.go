//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 231 — the database allows HTTP push without permitting HTTP pull.
func TestMigrationHTTPFunctionPushQueueBinding(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, accountID)
	for _, tc := range []struct {
		name, mode, class string
		wantCheck         bool
	}{
		{name: "http-push", mode: "push", class: "http"},
		{name: "http-pull", mode: "pull", class: "http", wantCheck: true},
		{name: "worker-pull", mode: "pull", class: "worker"},
		{name: "job-push", mode: "push", class: "job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `
				insert into queue_bindings (account_id, app_id, name, queue_name, mode, workload_class)
				values ($1, $2, $3, $3, $4, $5)`, accountID, appID, tc.name, tc.mode, tc.class)
			if !tc.wantCheck {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "queue_bindings_workload_class_chk" {
				t.Fatalf("error = %v, want queue binding class CHECK", err)
			}
		})
	}
}
