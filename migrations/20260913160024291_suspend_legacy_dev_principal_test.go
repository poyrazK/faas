//go:build !no_pg

package migrations_test

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationSuspendsLegacyDevPrincipalAndRevokesKeys(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpOnce(ctx, t, pool)

	var accountID string
	if err := pool.QueryRow(ctx, `
		insert into accounts (email, plan, status)
		values ('dev@local', 'scale', 'active')
		returning id::text
	`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	orgID := seedPersonalOrg(t, ctx, pool, accountID)
	if _, err := pool.Exec(ctx, `
		insert into api_keys (account_id, org_id, key_sha256, label, status)
		values ($1, $2, decode(repeat('ab', 32), 'hex'), 'legacy dev', 'active')
	`, accountID, orgID); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("20260913160024291_suspend_legacy_dev_principal.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Reapplying the data-only body simulates the migration encountering a
	// legacy principal. The Down section is an intentional no-op SELECT.
	for range 2 {
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply remediation: %v", err)
		}
	}
	var accountStatus, keyStatus string
	var revoked bool
	if err := pool.QueryRow(ctx, `
		select a.status, k.status, k.revoked_at is not null
		  from accounts a join api_keys k on k.account_id = a.id
		 where a.id = $1
	`, accountID).Scan(&accountStatus, &keyStatus, &revoked); err != nil {
		t.Fatal(err)
	}
	if accountStatus != "suspended" || keyStatus != "revoked" || !revoked {
		t.Fatalf("account=%s key=%s revoked_at=%v", accountStatus, keyStatus, revoked)
	}
}
