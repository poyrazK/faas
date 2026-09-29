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

// ADR-361: apps.egress_ports defaults empty, is range-checked, and a change
// bumps the shared app egress policy revision and notifies schedd, so live
// instances converge through the allowlist path.
func TestMigrations_AppEgressPortsColumnAndRevision(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	accountID, appID := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, plan, created_at) VALUES ($1, $2, 'pro', now())`,
		accountID, "egress-ports-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1, 'active', now())`, appID, accountID, "egress-ports-"+appID[:8]); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	var ports []int32
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT egress_ports, egress_allowlist_revision FROM apps WHERE id = $1`, appID).Scan(&ports, &revision); err != nil {
		t.Fatalf("read defaults: %v", err)
	}
	if len(ports) != 0 || revision != 1 {
		t.Fatalf("defaults = ports %v revision %d, want [] 1", ports, revision)
	}

	for _, bad := range []string{`ARRAY[0]`, `ARRAY[65536]`, `(SELECT array_agg(g) FROM generate_series(1000, 1064) g)`} {
		if _, err := pool.Exec(ctx, `UPDATE apps SET egress_ports = `+bad+` WHERE id = $1`, appID); err == nil {
			t.Fatalf("egress_ports = %s was accepted", bad)
		}
	}

	notifications, cancelListen, err := db.Subscribe(ctx, pool, []string{db.NotifyAppEgressPolicyChanged})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelListen()
	if _, err := pool.Exec(ctx, `UPDATE apps SET egress_ports = ARRAY[5432, 6379] WHERE id = $1`, appID); err != nil {
		t.Fatalf("set ports: %v", err)
	}
	select {
	case n := <-notifications:
		payload, err := db.ParseAppEgressPolicyChangedPayload(n.Payload)
		if err != nil || payload.AppID != appID || payload.Revision != 2 {
			t.Fatalf("notification = %+v (%v), want app %s revision 2", payload, err, appID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("egress port change did not notify schedd")
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET egress_ports = ARRAY[5432, 6379] WHERE id = $1`, appID); err != nil {
		t.Fatalf("write identical ports: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT egress_allowlist_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	if revision != 2 {
		t.Fatalf("revision = %d after one real change, want 2", revision)
	}
}
