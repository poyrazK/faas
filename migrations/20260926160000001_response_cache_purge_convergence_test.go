//go:build !no_pg

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrations_ResponseCachePurgeIsDurableAndNotified(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountID, appID := uuid.NewString(), uuid.NewString()
	slug := "app-cache-purge-" + appID[:8]
	nodeName := "cache-purge-test-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gateway_response_cache_purge_watermarks WHERE node_name = $1`, nodeName)
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, email, plan, created_at)
		VALUES ($1, $2, 'pro', now())
	`, accountID, "cache-purge-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1, 'active', now())
	`, appID, accountID, slug); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire notification listener: %v", err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "LISTEN "+db.NotifyCachePurge); err != nil {
		t.Fatalf("LISTEN cache purge: %v", err)
	}

	store := state.NewPgStore(pool)
	firstID, err := store.CreateResponseCachePurge(ctx, appID, "/products/*", "")
	if err != nil {
		t.Fatalf("create path purge: %v", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	notice, err := listener.Conn().WaitForNotification(tctx)
	if err != nil {
		t.Fatalf("wait for purge notification: %v", err)
	}
	if notice.Channel != db.NotifyCachePurge {
		t.Fatalf("notification channel = %q, want %q", notice.Channel, db.NotifyCachePurge)
	}
	var payload struct {
		ID       int64  `json:"id"`
		AppID    string `json:"app_id"`
		PathGlob string `json:"path_glob"`
		Tag      string `json:"tag"`
	}
	if err := json.Unmarshal([]byte(notice.Payload), &payload); err != nil {
		t.Fatalf("decode purge notification: %v", err)
	}
	if payload.ID != firstID || payload.AppID != appID || payload.PathGlob != "/products/*" || payload.Tag != "" {
		t.Fatalf("notification payload = %+v, want id %d app %s path /products/*", payload, firstID, appID)
	}

	secondID, err := store.CreateResponseCachePurge(ctx, appID, "", "Product:42")
	if err != nil {
		t.Fatalf("create tag purge: %v", err)
	}
	if secondID <= firstID {
		t.Fatalf("purge IDs = %d then %d; want monotonic order", firstID, secondID)
	}
	latest, err := store.LatestAppResponseCachePurgeID(ctx, appID)
	if err != nil || latest != secondID {
		t.Fatalf("latest app purge = %d, %v; want %d", latest, err, secondID)
	}
	changes, err := store.ListResponseCachePurgesAfter(ctx, firstID, 10)
	if err != nil || len(changes) != 1 || changes[0].ID != secondID || changes[0].Tag != "product:42" {
		t.Fatalf("purge replay after %d = %+v, %v; want canonical tag revision %d", firstID, changes, err, secondID)
	}
	if err := store.UpsertGatewayResponseCachePurgeWatermark(ctx, nodeName, secondID); err != nil {
		t.Fatalf("publish gateway watermark: %v", err)
	}
	if err := store.UpsertGatewayResponseCachePurgeWatermark(ctx, nodeName, firstID); err != nil {
		t.Fatalf("attempt watermark regression: %v", err)
	}
	cursor, err := store.BootstrapGatewayResponseCachePurgeCursor(ctx, nodeName)
	if err != nil || cursor != secondID {
		t.Fatalf("bootstrapped cursor = %d, %v; want monotonic %d", cursor, err, secondID)
	}
	if _, err := pool.Exec(ctx, `UPDATE response_cache_purge_change_log SET created_at = now() - interval '31 days' WHERE id = $1`, firstID); err != nil {
		t.Fatalf("age old purge row: %v", err)
	}
	if _, err := store.PruneResponseCachePurgeChangeLog(ctx, time.Now().UTC().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("prune purge history: %v", err)
	}
	latest, err = store.LatestAppResponseCachePurgeID(ctx, appID)
	if err != nil || latest != secondID {
		t.Fatalf("latest app purge after prune = %d, %v; want retained latest %d", latest, err, secondID)
	}
}
