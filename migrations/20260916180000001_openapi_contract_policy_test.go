//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_20260916180000001OpenAPIContractPolicy(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-202609161801', 'openapi-policy-migration@example.test', 'pro', now())
		on conflict (id) do nothing`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ('00000000-0000-0000-0000-202609161802', '00000000-0000-0000-0000-202609161801', 'openapi-policy-migration', 'app', 128, 1, 'active', now())
		on conflict (id) do nothing`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	var policy string
	if err := pool.QueryRow(ctx, `select openapi_contract_policy from apps where id = '00000000-0000-0000-0000-202609161802'`).Scan(&policy); err != nil {
		t.Fatalf("read default policy: %v", err)
	}
	if policy != "observe" {
		t.Fatalf("default policy = %q, want observe", policy)
	}
	if _, err := pool.Exec(ctx, `update apps set openapi_contract_policy = 'warn' where id = '00000000-0000-0000-0000-202609161802'`); err != nil {
		t.Fatalf("write warn policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `update apps set openapi_contract_policy = 'invalid' where id = '00000000-0000-0000-0000-202609161802'`); err == nil {
		t.Fatal("invalid policy update unexpectedly succeeded")
	}
}
