//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_GitHubOIDCBindingIDs(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	for _, column := range []string{"github_owner_id", "github_repo_id"} {
		var dataType string
		if err := pool.QueryRow(ctx, `
			select data_type from information_schema.columns
			 where table_name = 'apps' and column_name = $1`, column).Scan(&dataType); err != nil {
			t.Fatalf("column %s missing: %v", column, err)
		}
		if dataType != "bigint" {
			t.Errorf("column %s type = %q, want bigint", column, dataType)
		}
	}

	const accountID = "00000000-0000-0000-0000-000000000291"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan)
		values ($1::uuid, 'github-oidc-binding-ids@example.com', 'free')`, accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (account_id, slug, ram_mb, github_owner_id)
		values ($1::uuid, 'github-oidc-invalid-ids', 128, 123456)`, accountID); err == nil {
		t.Fatal("accepted a binding with only one immutable GitHub ID")
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (account_id, slug, ram_mb, github_repo_id)
		values ($1::uuid, 'github-oidc-invalid-owner', 128, 789012)`, accountID); err == nil {
		t.Fatal("accepted a binding with only a repository ID")
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (account_id, slug, ram_mb, github_owner_id, github_repo_id)
		values ($1::uuid, 'github-oidc-invalid-zero', 128, 0, 789012)`, accountID); err == nil {
		t.Fatal("accepted non-positive immutable GitHub IDs")
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (account_id, slug, ram_mb, github_owner_id, github_repo_id)
		values ($1::uuid, 'github-oidc-valid-ids', 128, 123456, 789012)`, accountID); err != nil {
		t.Fatalf("insert valid immutable IDs: %v", err)
	}
}
