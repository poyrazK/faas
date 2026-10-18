package db_test

// adr: 20261018084150

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// A per-unit release applies expand migrations before the next full release
// ships them, so a binary rolled back to the previous release restarts, and
// runs its startup MigrateUp, against a ledger with versions it does not
// embed. That must be a no-op, not a failure: the rollback depends on it.
func TestMigrateUpIgnoresLedgerVersionsTheBinaryDoesNotEmbed(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// A version newer than any this binary embeds, as a later release's
	// migration would leave it.
	const newer = int64(99991231235959999)
	if _, err := pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, newer); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp with a newer ledger: %v", err)
	}
	st, err := db.Status(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Pending) != 0 || st.DBVersion != newer {
		t.Fatalf("status = pending %v, db version %d; want none pending at %d", st.Pending, st.DBVersion, newer)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id = $1`, newer).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("ledger rows for %d = %d, want 1", newer, rows)
	}
}
