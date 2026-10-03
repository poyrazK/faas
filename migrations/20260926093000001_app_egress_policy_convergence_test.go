//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_AppEgressPolicyRevisionTracksDesiredChanges(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountID, appID := uuid.NewString(), uuid.NewString()
	slug := "app-egress-rev-" + appID[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, email, plan, created_at)
		VALUES ($1, $2, 'pro', now())
	`, accountID, "app-egress-rev-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1, 'active', now())
	`, appID, accountID, slug); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	var revision int64
	if err := pool.QueryRow(ctx, `SELECT egress_allowlist_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
		t.Fatalf("read initial revision: %v", err)
	}
	if revision != 1 {
		t.Fatalf("initial revision = %d, want 1", revision)
	}
	notifications, cancelListen, err := db.Subscribe(ctx, pool, []string{db.NotifyAppEgressPolicyChanged})
	if err != nil {
		t.Fatalf("subscribe to app egress policy notifications: %v", err)
	}
	defer cancelListen()
	if _, err := pool.Exec(ctx, `UPDATE apps SET egress_allowlist = ARRAY['8.8.8.0/24'::cidr] WHERE id = $1`, appID); err != nil {
		t.Fatalf("change allowlist: %v", err)
	}
	select {
	case notification := <-notifications:
		payload, err := db.ParseAppEgressPolicyChangedPayload(notification.Payload)
		if err != nil || payload.AppID != appID || payload.Revision != 2 {
			t.Fatalf("notification = %+v (%v), want app %s revision 2", payload, err, appID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("allowlist change did not emit the low-latency notification")
	}
	if err := pool.QueryRow(ctx, `SELECT egress_allowlist_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
		t.Fatalf("read changed revision: %v", err)
	}
	if revision != 2 {
		t.Fatalf("revision after allowlist change = %d, want 2", revision)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET egress_allowlist = ARRAY['8.8.8.0/24'::cidr] WHERE id = $1`, appID); err != nil {
		t.Fatalf("write identical allowlist: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET max_concurrency = 2 WHERE id = $1`, appID); err != nil {
		t.Fatalf("update unrelated setting: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET egress_allowlist_revision = 99 WHERE id = $1`, appID); err != nil {
		t.Fatalf("attempt to forge revision: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT egress_allowlist_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
		t.Fatalf("read stable revision: %v", err)
	}
	if revision != 2 {
		t.Fatalf("revision after no-op/unrelated/forged writes = %d, want 2", revision)
	}

	var nodeStatusTable int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'app_egress_policy_node_status'`).Scan(&nodeStatusTable); err != nil {
		t.Fatalf("check node status table: %v", err)
	}
	if nodeStatusTable != 1 {
		t.Fatal("app_egress_policy_node_status table missing")
	}
}
