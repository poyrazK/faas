package state_test

// adr: 595 Creating-account attribution is immutable for every current writer.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedLegacyForeignCreatingAccount models a historical ownership change from
// before ADR-595. Only the isolated pgtest schema may seed that legacy state;
// the current immutable-account trigger is checked and restored transactionally.
// Readers and replay admission must still refuse cross-tenant historical data.
func seedLegacyForeignCreatingAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appID, accountID string) {
	t.Helper()
	var schema, database string
	if err := pool.QueryRow(ctx, `SELECT current_schema(),current_database()`).Scan(&schema, &database); err != nil || !strings.HasPrefix(schema, "faas_test_") && !strings.HasPrefix(database, "faas_test_clone_") {
		t.Fatal("legacy fixture requires an isolated pgtest schema or database", schema, database, err)
	}
	_, err := pool.Exec(ctx, `UPDATE apps SET account_id=$1 WHERE id=$2`, accountID, appID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "application_standard_app_account_identity" {
		t.Fatal("current writer bypassed immutable creating account", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `ALTER TABLE apps DISABLE TRIGGER application_standard_app_account_identity_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE apps SET account_id=$1 WHERE id=$2`, accountID, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE apps ENABLE TRIGGER application_standard_app_account_identity_guard`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := pool.QueryRow(ctx, `SELECT tgenabled='O' FROM pg_trigger WHERE tgrelid='apps'::regclass AND tgname='application_standard_app_account_identity_guard'`).Scan(&enabled); err != nil || !enabled {
		t.Fatal("legacy fixture did not restore current identity guard", err)
	}
}
