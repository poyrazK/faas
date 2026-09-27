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

func TestMigrations_AppCPUPolicyRevisionTracksDesiredChanges(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountID, appID := uuid.NewString(), uuid.NewString()
	slug := "app-cpu-rev-" + appID[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, email, plan, created_at)
		VALUES ($1, $2, 'pro', now())
	`, accountID, "app-cpu-rev-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO apps (id, account_id, slug, type, ram_mb, cpu_millicores, max_concurrency, status, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1000, 1, 'active', now())
	`, appID, accountID, slug); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT app_cpu_policy_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
		t.Fatalf("read initial revision: %v", err)
	}
	if revision != 1 {
		t.Fatalf("initial revision = %d, want 1", revision)
	}
	notifications, cancelListen, err := db.Subscribe(ctx, pool, []string{db.NotifyAppCPULimitPolicyChanged})
	if err != nil {
		t.Fatalf("subscribe to app CPU policy notifications: %v", err)
	}
	defer cancelListen()
	if _, err := pool.Exec(ctx, `UPDATE apps SET cpu_millicores = 500 WHERE id = $1`, appID); err != nil {
		t.Fatalf("change CPU quota: %v", err)
	}
	select {
	case notification := <-notifications:
		payload, err := db.ParseAppCPULimitPolicyChangedPayload(notification.Payload)
		if err != nil || payload.AppID != appID || payload.Revision != 2 {
			t.Fatalf("notification = %+v (%v), want app %s revision 2", payload, err, appID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CPU quota change did not emit the low-latency notification")
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET max_concurrency = 2 WHERE id = $1`, appID); err != nil {
		t.Fatalf("update unrelated setting: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET app_cpu_policy_revision = 99 WHERE id = $1`, appID); err != nil {
		t.Fatalf("attempt to forge revision: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT app_cpu_policy_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
		t.Fatalf("read stable revision: %v", err)
	}
	if revision != 2 {
		t.Fatalf("revision after unrelated/forged writes = %d, want 2", revision)
	}
	var statusTable int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'app_cpu_policy_node_status'`).Scan(&statusTable); err != nil {
		t.Fatalf("check node status table: %v", err)
	}
	if statusTable != 1 {
		t.Fatal("app_cpu_policy_node_status table missing")
	}
}
