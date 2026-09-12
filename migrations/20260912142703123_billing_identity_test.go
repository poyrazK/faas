//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestBillingIdentityMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	for _, column := range []string{"business_name", "billing_address", "tax_id"} {
		var nullable string
		if err := pool.QueryRow(ctx, `
			select is_nullable from information_schema.columns
			 where table_schema = current_schema() and table_name = 'accounts' and column_name = $1`, column).Scan(&nullable); err != nil {
			t.Fatalf("column %s: %v", column, err)
		}
		if nullable != "YES" {
			t.Errorf("column %s is_nullable = %q, want YES", column, nullable)
		}
	}
	for _, constraint := range []string{
		"accounts_business_name_length_chk",
		"accounts_billing_address_length_chk",
		"accounts_tax_id_length_chk",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `select exists (select 1 from pg_constraint where conname = $1)`, constraint).Scan(&exists); err != nil {
			t.Fatalf("constraint %s: %v", constraint, err)
		}
		if !exists {
			t.Errorf("missing constraint %s", constraint)
		}
	}
}
