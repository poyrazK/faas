//go:build !no_pg

package migrations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 601
func TestMigrationPlainReplayAdoptsTrustedLatestChild(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261005101735681)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "plain-replay-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "plain-replay-migration", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, root.ID, "legacy failure", 0, 0); err != nil {
		t.Fatal(err)
	}
	enqueue := func(parent string) state.Invocation {
		t.Helper()
		child, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID,
			DeploymentScope: root.DeploymentScope, Source: state.InvocationReplay, ReplayedFromInvocationID: parent,
			Headers: []byte(`{"X-Gregale-Replayed-From":"` + root.ID + `"}`), DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return child
	}
	older, latest, corrupt, unlinked := enqueue(root.ID), enqueue(root.ID), enqueue(root.ID), enqueue("")
	if _, err := pool.Exec(ctx, "update invocations set replay_root_created_at=replay_root_created_at-interval '1 second' where id=$1", corrupt.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	child, err := store.ExistingPlainInvocationReplay(ctx, account.ID, root.ID)
	if err != nil || child.ID != latest.ID {
		t.Fatalf("upgrade did not adopt latest trusted replay: %+v %v", child, err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from invocation_plain_replays").Scan(&count); err != nil || count != 1 {
		t.Fatalf("upgrade invented replay identities: %d %v", count, err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from invocations where id=any($1::uuid[])", []string{older.ID, corrupt.ID, unlinked.ID}).Scan(&count); err != nil || count != 3 {
		t.Fatalf("upgrade altered retained history: %d %v", count, err)
	}
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{latest.ID}); err != nil {
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
	if _, err := store.ReplayPlainInvocation(ctx, account.ID, root.ID, state.PlainInvocationReplayOptions{}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("upgrade replay replaced durable pruned identity: %v", err)
	}
	if _, err := store.DeleteInvocationsByIDs(ctx, []string{root.ID}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from invocation_plain_replays").Scan(&count); err != nil || count != 0 {
		t.Fatalf("parent retention left an orphan marker: %d %v", count, err)
	}
}
