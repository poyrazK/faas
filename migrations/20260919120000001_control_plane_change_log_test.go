//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_ControlPlaneChangeLogRecordsAppLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountID := uuid.NewString()
	appID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from apps where id = $1`, appID)
		_, _ = pool.Exec(ctx, `delete from control_plane_change_log where app_id = $1`, appID)
		_, _ = pool.Exec(ctx, `delete from accounts where id = $1`, accountID)
	})

	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, $2, 'pro', now())
	`, accountID, "control-plane-ledger-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ($1, $2, $3, 'app', 128, 1, 'active', now())
	`, appID, accountID, "control-plane-ledger-"+appID); err != nil {
		t.Fatalf("insert app: %v", err)
	}
	if _, err := pool.Exec(ctx, `update apps set slug = slug || '-updated' where id = $1`, appID); err != nil {
		t.Fatalf("update app: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from apps where id = $1`, appID); err != nil {
		t.Fatalf("delete app: %v", err)
	}

	rows, err := pool.Query(ctx, `
		select resource_type, resource_id, app_id, operation
		from control_plane_change_log
		where app_id = $1
		order by id
	`, appID)
	if err != nil {
		t.Fatalf("read control-plane ledger: %v", err)
	}
	defer rows.Close()
	var operations []string
	for rows.Next() {
		var resourceType, resourceID, loggedAppID, operation string
		if err := rows.Scan(&resourceType, &resourceID, &loggedAppID, &operation); err != nil {
			t.Fatalf("scan control-plane ledger: %v", err)
		}
		if resourceType != "app" || resourceID != appID || loggedAppID != appID {
			t.Fatalf("ledger identity = (%q, %q, %q), want app/%s/%s", resourceType, resourceID, loggedAppID, appID, appID)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate control-plane ledger: %v", err)
	}
	want := []string{"created", "updated", "deleted"}
	if len(operations) != len(want) {
		t.Fatalf("ledger operations = %v, want %v", operations, want)
	}
	for i := range want {
		if operations[i] != want[i] {
			t.Fatalf("ledger operations = %v, want %v", operations, want)
		}
	}
}
