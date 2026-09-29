//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// ADR-361 decision 9: egress_flood is a valid account abuse hold reason.
func TestMigrations_AccountAbuseHoldFloodReason(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	accountID := uuid.NewString()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID) })
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan, created_at) VALUES ($1, $2, 'pro', now())`,
		accountID, "abuse-flood-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET abuse_hold_at = now(), abuse_hold_reason = 'egress_flood' WHERE id = $1`, accountID); err != nil {
		t.Fatalf("hold egress_flood: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET abuse_hold_reason = 'billing' WHERE id = $1`, accountID); err == nil {
		t.Fatal("an unknown hold reason was accepted")
	}
}
