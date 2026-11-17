package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// production 2026-11-16: a peer schedd's replay worker claimed a credential
// rotation's restart, deferred it because another node owned the app, and
// kept winning the row back; the owner never ran it. With the app's owner
// named, a schedd claims only the wakes and restarts of apps it owns.
func TestAppOwnerClaimsSkipRowsOfAppsAnotherNodeOwns(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "outbox-owner@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	nodeA, nodeB := computeNode(t, pool, "owner-a"), computeNode(t, pool, "owner-b")
	ownedByA := appOn(t, store, pool, account.ID, "owned-by-a", nodeA)
	unplaced := appOn(t, store, pool, account.ID, "unplaced", "")
	channels := []string{db.NotifyAppWake, db.NotifyRuntimeConfigRestart, db.NotifySnapshotPrime}

	restart := queue(t, pool, db.NotifyRuntimeConfigRestart, fmt.Sprintf(`{"app_id":%q,"wake_id":"w1"}`, ownedByA))
	if item, err := db.ClaimNotificationForAppOwner(ctx, pool, "schedd", nodeB, channels, time.Minute); !errors.Is(err, db.ErrNotificationOutboxEmpty) {
		t.Fatalf("node B claimed node A's restart: %+v, %v", item, err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM notification_outbox WHERE id=$1`, restart).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("a skipped row changed: attempts=%d err=%v", attempts, err)
	}
	claimAndComplete(t, pool, nodeA, channels, restart)

	// Apps with no node, poison payloads and other channels stay claimable
	// by any schedd: the handler reports them.
	for _, row := range []struct{ channel, payload string }{
		{db.NotifyAppWake, fmt.Sprintf(`{"app_id":%q,"wake_id":"w2"}`, unplaced)},
		{db.NotifyAppWake, `{"app_id":"not-a-uuid"}`},
		{db.NotifyRuntimeConfigRestart, `not json`},
		{db.NotifySnapshotPrime, fmt.Sprintf(`{"app_id":%q}`, ownedByA)},
	} {
		claimAndComplete(t, pool, nodeB, channels, queue(t, pool, row.channel, row.payload))
	}
	// An unnamed owner keeps the old claim-everything behaviour.
	id := queue(t, pool, db.NotifyAppWake, fmt.Sprintf(`{"app_id":%q,"wake_id":"w3"}`, ownedByA))
	claimAndComplete(t, pool, "", channels, id)
}

func computeNode(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb)
		VALUES($1,'unix:///run/test.sock',2,1024,10,512) RETURNING id::text`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func appOn(t *testing.T, store *state.PgStore, pool *pgxpool.Pool, accountID, slug, nodeID string) string {
	t.Helper()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: accountID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE apps SET node_id=NULLIF($2,'')::uuid WHERE id=$1::uuid`, app.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	return app.ID
}

func queue(t *testing.T, pool *pgxpool.Pool, channel, payload string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO notification_outbox(channel,payload,available_at) VALUES($1,$2,now()-interval '1 second') RETURNING id`, channel, payload).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func claimAndComplete(t *testing.T, pool *pgxpool.Pool, owner string, channels []string, want int64) {
	t.Helper()
	item, err := db.ClaimNotificationForAppOwner(t.Context(), pool, "schedd", owner, channels, time.Minute)
	if err != nil || item.ID != want {
		t.Fatalf("owner %q claimed %+v, %v; want row %d", owner, item, err, want)
	}
	if err := db.CompleteNotification(t.Context(), pool, item.ID, item.ClaimToken); err != nil {
		t.Fatal(err)
	}
}
