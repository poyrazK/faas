//go:build !no_pg

// adr: 1346
package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestQueueReleaseRequirementsReplayPreservesCanaryValidation(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	assertValidator := func() {
		t.Helper()
		var valid bool
		err := pool.QueryRow(ctx, `SELECT
 valid_queue_release_requirements('{"events":"handler_completion"}'::jsonb)
 AND NOT valid_queue_release_requirements('{"events":"unknown"}'::jsonb)`).Scan(&valid)
		if err != nil || !valid {
			t.Fatalf("replay changed the canary validator: valid=%v err=%v", valid, err)
		}
	}
	assertValidator()
	raw, err := migrations.FS.ReadFile("20261016150020001_queue_release_requirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("migration has no Down boundary")
	}
	// Replay the earlier DDL while the later canary migration remains applied.
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("replay queue requirements: %v", err)
	}
	assertValidator()
}
