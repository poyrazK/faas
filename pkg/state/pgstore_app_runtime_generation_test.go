//go:build !no_pg

// adr: 1741 — the generation's notification arrives at commit, never for a
// rolled-back change, and deleting an app with its triggers works.
package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppRuntimeGenerationNotifiesAtCommit(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "listen "+db.NotifyAppRuntimeChanged); err != nil {
		t.Fatal(err)
	}
	next := func(wait time.Duration) (string, int64, bool) {
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		n, err := listener.Conn().WaitForNotification(waitCtx)
		if err != nil {
			return "", 0, false
		}
		var payload struct {
			AppID      string `json:"app_id"`
			Generation int64  `json:"generation"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil {
			t.Fatalf("payload %q: %v", n.Payload, err)
		}
		return payload.AppID, payload.Generation, true
	}

	before, err := store.AppRuntimeGeneration(ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack(t, ctx, pool, f.app.ID)
	if appID, _, ok := next(300 * time.Millisecond); ok {
		t.Fatalf("a rolled-back change announced app %s", appID)
	}
	ram := 512
	if _, err := store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
	// One write may bump the generation more than once (triggers cascade);
	// the last announcement carries the stored value.
	appID, generation, ok := next(3 * time.Second)
	if !ok || appID != f.app.ID || generation <= before {
		t.Fatalf("notification = %s %d %v; want %s after %d", appID, generation, ok, f.app.ID, before)
	}
	for {
		laterApp, later, more := next(300 * time.Millisecond)
		if !more {
			break
		}
		if laterApp == f.app.ID {
			generation = later
		}
	}
	if got, err := store.AppRuntimeGeneration(ctx, f.app.ID); err != nil || got != generation {
		t.Fatalf("stored generation = %d, %v; want the last announced %d", got, err, generation)
	}
	if err := store.DeleteApp(ctx, f.app.ID); err != nil {
		t.Fatalf("delete an app with generation triggers: %v", err)
	}
}

// rolledBack changes the app inside a transaction it then rolls back.
func rolledBack(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appID string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE apps SET ram_mb = ram_mb * 2 WHERE id = $1`, appID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}
