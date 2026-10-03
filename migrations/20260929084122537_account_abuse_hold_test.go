//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ADR-361: the account abuse hold is a reason + timestamp pair from a closed
// set, and it never touches the billing status.
func TestMigrations_AccountAbuseHold(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	accountID := uuid.NewString()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID) })
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan, created_at) VALUES ($1, $2, 'pro', now())`,
		accountID, "abuse-hold-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	for _, bad := range []string{
		`abuse_hold_at = now()`,
		`abuse_hold_reason = 'egress_fanout'`,
		`abuse_hold_at = now(), abuse_hold_reason = 'billing'`,
	} {
		if _, err := pool.Exec(ctx, `UPDATE accounts SET `+bad+` WHERE id = $1`, accountID); err == nil {
			t.Fatalf("UPDATE accounts SET %s was accepted", bad)
		}
	}
	for _, reason := range []string{"egress_fanout", "operator"} {
		if _, err := pool.Exec(ctx, `UPDATE accounts SET abuse_hold_at = now(), abuse_hold_reason = $2 WHERE id = $1`, accountID, reason); err != nil {
			t.Fatalf("hold %s: %v", reason, err)
		}
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM accounts WHERE id = $1`, accountID).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status after hold = %q, %v; want active", status, err)
	}
}
