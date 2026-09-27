//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_CorsPresetChangesAreDurableAndAccountScoped(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountA, accountB, presetID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM cors_presets WHERE id = $1`, presetID)
		_, _ = pool.Exec(ctx, `DELETE FROM cors_preset_change_log WHERE account_id = ANY($1::uuid[])`, []string{accountA, accountB})
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = ANY($1::uuid[])`, []string{accountA, accountB})
	})
	for i, accountID := range []string{accountA, accountB} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO accounts (id, email, plan, created_at)
			VALUES ($1, $2, 'pro', now())
		`, accountID, "cors-ledger-"+accountID+"@example.com"); err != nil {
			t.Fatalf("seed account %d: %v", i, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cors_presets (id, account_id, name, allow_origins, allow_methods)
		VALUES ($1, $2, 'shared', ARRAY['https://first.example'], ARRAY['GET'])
	`, presetID, accountA); err != nil {
		t.Fatalf("insert preset: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE cors_presets SET allow_origins = ARRAY['https://second.example'] WHERE id = $1
	`, presetID); err != nil {
		t.Fatalf("update preset: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback check: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE cors_presets SET allow_methods = ARRAY['POST'] WHERE id = $1
	`, presetID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("update preset in rollback check: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback preset update: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE cors_presets SET account_id = $2 WHERE id = $1`, presetID, accountB); err != nil {
		t.Fatalf("move preset between account scopes: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM cors_presets WHERE id = $1`, presetID); err != nil {
		t.Fatalf("delete preset: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT account_id::text, preset_id::text, operation
		FROM cors_preset_change_log
		WHERE preset_id = $1
		ORDER BY id
	`, presetID)
	if err != nil {
		t.Fatalf("read CORS preset ledger: %v", err)
	}
	defer rows.Close()
	type entry struct{ accountID, presetID, operation string }
	var got []entry
	for rows.Next() {
		var change entry
		if err := rows.Scan(&change.accountID, &change.presetID, &change.operation); err != nil {
			t.Fatalf("scan CORS preset ledger: %v", err)
		}
		got = append(got, change)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate CORS preset ledger: %v", err)
	}
	want := []entry{
		{accountA, presetID, "created"},
		{accountA, presetID, "updated"},
		{accountA, presetID, "updated"},
		{accountB, presetID, "updated"},
		{accountB, presetID, "deleted"},
	}
	if len(got) != len(want) {
		t.Fatalf("CORS preset ledger has %d rows: %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CORS preset ledger row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
