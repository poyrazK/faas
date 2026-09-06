//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_DeploymentsSourceSHA256(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	var dataType, nullable string
	if err := pool.QueryRow(ctx, `
		select data_type, is_nullable
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'deployments'
		   and column_name = 'source_sha256'`).Scan(&dataType, &nullable); err != nil {
		t.Fatalf("source_sha256 column: %v", err)
	}
	if dataType != "text" || nullable != "YES" {
		t.Fatalf("source_sha256 shape = (%s, %s), want (text, YES)", dataType, nullable)
	}

	var constraintExists bool
	if err := pool.QueryRow(ctx, `
		select exists (
			select 1 from pg_constraint
			 where conrelid = 'deployments'::regclass
			   and conname = 'deployments_source_sha256_shape_chk'
		)`).Scan(&constraintExists); err != nil {
		t.Fatalf("source_sha256 constraint: %v", err)
	}
	if !constraintExists {
		t.Fatal("source_sha256 shape constraint missing")
	}
}
