//go:build !no_pg

package migrations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 601
func TestMigrationPlainReplayAdoptsTrustedLatestChild(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261005101735681)
	// Seed the pre-upgrade schema directly: current enqueue paths depend on
	// tables introduced after this migration, and cannot model legacy rows.
	accountID, appID, rootID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	rootCreated := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, "INSERT INTO accounts (id,email,plan) VALUES ($1,$2,'pro')", accountID, accountID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO apps (id,account_id,slug,ram_mb) VALUES ($1,$2,'plain-replay-migration',256)", appID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invocations (id,account_id,app_id,source,state,deployment_scope,created_at)
		VALUES ($1,$2,$3,'async_invoke','failed','production',$4)`, rootID, accountID, appID, rootCreated); err != nil {
		t.Fatal(err)
	}
	enqueue := func(parent string, createdAt time.Time) string {
		t.Helper()
		childID := uuid.NewString()
		var parentID, replayRootID, replayRootCreated any
		if parent != "" {
			parentID, replayRootID, replayRootCreated = parent, rootID, rootCreated
		}
		if _, err := pool.Exec(ctx, `INSERT INTO invocations
			(id,account_id,app_id,source,deployment_scope,replayed_from_invocation_id,
			replay_root_invocation_id,replay_root_created_at,headers,created_at)
			VALUES ($1,$2,$3,'replay','production',$4,$5,$6,
			jsonb_build_object('X-Gregale-Replayed-From',$7::text),$8)`,
			childID, accountID, appID, parentID, replayRootID, replayRootCreated, rootID, createdAt); err != nil {
			t.Fatal(err)
		}
		return childID
	}
	older := enqueue(rootID, rootCreated.Add(time.Second))
	latest := enqueue(rootID, rootCreated.Add(2*time.Second))
	corrupt := enqueue(rootID, rootCreated.Add(3*time.Second))
	unlinked := enqueue("", rootCreated.Add(4*time.Second))
	if _, err := pool.Exec(ctx, "update invocations set replay_root_created_at=replay_root_created_at-interval '1 second' where id=$1", corrupt); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	child, err := store.ExistingPlainInvocationReplay(ctx, accountID, rootID)
	if err != nil || child.ID != latest {
		t.Fatalf("upgrade did not adopt latest trusted replay: %+v %v", child, err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from invocation_plain_replays").Scan(&count); err != nil || count != 1 {
		t.Fatalf("upgrade invented replay identities: %d %v", count, err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from invocations where id=any($1::uuid[])", []string{older, corrupt, unlinked}).Scan(&count); err != nil || count != 3 {
		t.Fatalf("upgrade altered retained history: %d %v", count, err)
	}
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{latest}); err != nil {
		t.Fatal(err)
	}
	// Reapplying an upgrade must preserve the pruned child's marker, even when
	// an older fork remains as a new backfill candidate.
	if _, err := pool.Exec(ctx, "delete from goose_db_version where version_id=20261005115846571"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplayPlainInvocation(ctx, accountID, rootID, state.PlainInvocationReplayOptions{}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("upgrade replay replaced durable pruned identity: %v", err)
	}
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{rootID}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from invocation_plain_replays").Scan(&count); err != nil || count != 0 {
		t.Fatalf("parent retention left an orphan marker: %d %v", count, err)
	}
}
