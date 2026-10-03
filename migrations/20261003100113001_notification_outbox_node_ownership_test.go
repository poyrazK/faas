//go:build !no_pg

// adr: 462 — owner routing installs over existing poison events and reverses.
package migrations_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestNotificationOutboxOwnershipMigrationPreservesPendingWork(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261003100113001_notification_outbox_node_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("ownership migration has no down section")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("remove ownership routing: %v", err)
	}
	var largeNode strings.Builder
	for i := range 96 {
		_, _ = fmt.Fprintf(&largeNode, "%x", sha256.Sum256([]byte(fmt.Sprint(i))))
	}
	for _, payload := range []string{"{\"node_id\":\"node-b\"}", "{}", "{bad-json", "{\"node_id\":\"" + largeNode.String() + "\"}"} {
		if _, err := pool.Exec(ctx, "INSERT INTO notification_outbox (channel, payload) VALUES ('snapshot_boot', $1)", payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("install routing over existing poison event: %v", err)
	}
	var untouched int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM notification_outbox WHERE state='pending' AND attempts=0").Scan(&untouched); err != nil || untouched != 4 {
		t.Fatalf("migration altered pending work: count=%d err=%v", untouched, err)
	}
	var valid bool
	if err := pool.QueryRow(ctx, "SELECT indisvalid FROM pg_index WHERE indexrelid='notification_outbox_node_claim_idx'::regclass").Scan(&valid); err != nil || !valid {
		t.Fatalf("ownership index invalid: valid=%v err=%v", valid, err)
	}
	item, err := db.ClaimNotificationForNode(ctx, pool, "imaged", "node-a", []string{db.NotifySnapshotBoot}, 0)
	if err != nil || item.Payload != "{}" {
		t.Fatalf("new routing lost legacy work or claimed sibling: item=%+v err=%v", item, err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("rollback ownership routing: %v", err)
	}
	var removed bool
	if err := pool.QueryRow(ctx, "SELECT to_regprocedure('notification_outbox_target_node(text,text)') IS NULL AND to_regclass('notification_outbox_node_claim_idx') IS NULL").Scan(&removed); err != nil || !removed {
		t.Fatalf("ownership objects remained after down: removed=%v err=%v", removed, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("reinstall ownership routing: %v", err)
	}
	var total int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM notification_outbox").Scan(&total); err != nil || total != 4 {
		t.Fatalf("down/up lost handoffs: count=%d err=%v", total, err)
	}
}
