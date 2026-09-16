//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const expireOrphanedCLILoginKeysVersion int64 = 20260916220000001

func TestMigrations_ExpireOnlyOrphanedCLILoginKeys(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	accountID := seedAccount(t, ctx, pool)
	var orgID string
	if err := pool.QueryRow(ctx, `select id from orgs where personal_owner_account_id=$1`, accountID).Scan(&orgID); err != nil {
		t.Fatalf("personal org: %v", err)
	}
	keyOrdinal := byte(0)
	insert := func(label string, expires *time.Time) string {
		t.Helper()
		keyOrdinal++
		var id string
		if err := pool.QueryRow(ctx, `
			insert into api_keys(account_id, org_id, key_sha256, label, scopes, expires_at)
			values ($1,$2,$3,$4,array['admin'],$5)
			returning id`, accountID, orgID, []byte{keyOrdinal}, label, expires).Scan(&id); err != nil {
			t.Fatalf("insert key %q: %v", label, err)
		}
		return id
	}
	orphaned := insert("cli-login", nil)
	userKey := insert("production-ci", nil)
	alreadyBound := time.Now().UTC().Add(7 * 24 * time.Hour)
	bounded := insert("cli-login", &alreadyBound)

	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=$1`, expireOrphanedCLILoginKeysVersion); err != nil {
		t.Fatalf("rewind migration: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	readExpiry := func(id string) *time.Time {
		t.Helper()
		var got *time.Time
		if err := pool.QueryRow(ctx, `select expires_at from api_keys where id=$1`, id).Scan(&got); err != nil {
			t.Fatalf("read expiry: %v", err)
		}
		return got
	}
	got := readExpiry(orphaned)
	if got == nil || got.Before(time.Now().Add(29*24*time.Hour)) || got.After(time.Now().Add(31*24*time.Hour)) {
		t.Fatalf("orphaned CLI expiry = %v, want about 30 days", got)
	}
	if got := readExpiry(userKey); got != nil {
		t.Fatalf("user-created key expiry changed to %v", got)
	}
	if got := readExpiry(bounded); got == nil || got.Sub(alreadyBound) > time.Second || alreadyBound.Sub(*got) > time.Second {
		t.Fatalf("pre-bounded CLI expiry changed: got %v want %v", got, alreadyBound)
	}
}
