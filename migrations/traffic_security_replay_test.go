//go:build !no_pg

// adr: 531 — replay must preserve revocation generations and deleted tombstones.
package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestTrafficSecurityMigrationReplayPreservesGenerations(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID, tombstoneID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts(id,email,plan,status) VALUES($1,$2,'free','suspended')`, accountID, accountID+"@replay.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE traffic_security_epochs SET revision=77 WHERE scope_kind='account' AND scope_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO traffic_security_epochs(scope_kind,scope_id,revision,revoked,reason) VALUES('app',$1,55,true,'entity_deleted')`, tombstoneID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=20260929230709001`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay: %v", err)
	}
	assertEpoch := func(kind, id string, wantRevision int64, wantRevoked bool, wantReason string) {
		t.Helper()
		var revision int64
		var revoked bool
		var reason string
		if err := pool.QueryRow(ctx, `SELECT revision,revoked,reason FROM traffic_security_epochs WHERE scope_kind=$1 AND scope_id=$2`, kind, id).Scan(&revision, &revoked, &reason); err != nil {
			t.Fatal(err)
		}
		if revision != wantRevision || revoked != wantRevoked || reason != wantReason {
			t.Fatalf("%s epoch=(%d,%t,%s), want=(%d,%t,%s)", kind, revision, revoked, reason, wantRevision, wantRevoked, wantReason)
		}
	}
	assertEpoch("account", accountID, 77, true, "account_suspended")
	assertEpoch("app", tombstoneID, 55, true, "entity_deleted")
	if _, err := pool.Exec(ctx, `UPDATE accounts SET status='active' WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	assertEpoch("account", accountID, 78, false, "released")
}
