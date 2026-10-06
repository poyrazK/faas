//go:build !no_pg

// adr: 623 — additive promotion state can be replayed and reversed.
package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestBindingProjectPromotionMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	migrateUpOnce(ctx, t, pool)
	source, err := os.ReadFile("20261006112425001_binding_checked_environment_promotion.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	for _, sql := range []string{up, down, up, up} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='project_environment_promotions' AND column_name IN ('bindings_required','bindings_check','binding_check_next_at','binding_worker_token','binding_worker_until')").Scan(&count); err != nil || count != 5 {
		t.Fatalf("incomplete promotion state: %d %v", count, err)
	}
}
