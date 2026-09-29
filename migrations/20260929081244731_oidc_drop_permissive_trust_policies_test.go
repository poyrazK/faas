//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const oidcPermissivePoliciesPrevious int64 = 20260928193449782

// Legacy first-use trust policies (empty subject_pattern / audience)
// admitted any token from the issuer; the migration removes them and keeps
// pinned policies.
func TestMigrations_OIDCDropPermissiveTrustPolicies(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, oidcPermissivePoliciesPrevious)
	legacy := seedAccount(t, ctx, pool)
	pinned := seedAccount(t, ctx, pool)
	const issuer = "https://token.actions.githubusercontent.com"
	for _, row := range []struct {
		account, pattern string
		audience         []string
	}{
		{legacy, "", []string{}},
		{pinned, "^repo:acme/app:ref:refs/heads/main$", []string{"gregale"}},
	} {
		if _, err := pool.Exec(ctx, `insert into oidc_trust_policies
			(account_id, issuer_url, jwks_url, audience, subject_pattern, algorithms, audit_login)
			values ($1, $2, $2 || '/.well-known/jwks', $3, $4, array['RS256'], 'auto')`,
			row.account, issuer, row.audience, row.pattern); err != nil {
			t.Fatalf("seed policy: %v", err)
		}
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var remaining []string
	rows, err := pool.Query(ctx, `select account_id::text from oidc_trust_policies order by account_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		remaining = append(remaining, id)
	}
	rows.Close()
	if len(remaining) != 1 || remaining[0] != pinned {
		t.Fatalf("remaining policies = %v, want only the pinned account %s", remaining, pinned)
	}
}
