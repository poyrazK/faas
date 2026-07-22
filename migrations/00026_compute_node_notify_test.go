//go:build !no_pg

// Migration-apply test for 00026 (compute_nodes → compute_node_changed
// pg_notify trigger). Pins the load-bearing contract from issue #98 /
// ADR-028:
//
//   1. The migration set applies cleanly through 00026.
//   2. INSERT into compute_nodes fires compute_node_changed with the
//      new row's id and active=true. gatewayd's per-node client cache
//      eviction hook keys on this payload — a regression that drops
//      the trigger would leave stale conns cached past a node's IP
//      rotation.
//   3. UPDATE on compute_nodes (SetComputeNodeActive path) fires the
//      same channel with the post-update active flag, so the watchdog
//      and heartbeat goroutine paths both surface in gatewayd's cache.
//
// The test subscribes via db.Subscribe before issuing the writes so
// the LISTEN connection is parked on the channel; pg_notify fires
// synchronously inside the transaction commit, so the notification is
// observable from a second connection only AFTER the writer commits.
// We pin a generous read timeout (5s) to absorb the worst-case CI
// latency; the channel emits within milliseconds on a healthy box.
//
// Build tag mirrors 00024_compute_nodes_test.go:26.

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00026_ComputeNodeNotify pins the trigger contract.
// Two writes are observed: a fresh INSERT (UpsertComputeNode path)
// and an UPDATE (SetComputeNodeActive / heartbeat path). Both must
// surface on compute_node_changed with payload {node_id, active}.
func TestMigrations_00026_ComputeNodeNotify(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	notif, cancel, err := db.Subscribe(ctx, pool, []string{db.NotifyComputeNodeChanged})
	if err != nil {
		t.Fatalf("Subscribe(compute_node_changed): %v", err)
	}
	defer cancel()

	// (1) INSERT — UpsertComputeNode's new-row path. active defaults
	// to true at the column level so the payload's active field
	// must be true here.
	var newID string
	if err := pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb)
		values ('notif-test-node', 'unix:///run/faas/notif.sock', 8, 4096, 16, 2048)
		returning id
	`).Scan(&newID); err != nil {
		t.Fatalf("insert compute_node: %v", err)
	}

	got := waitForNotification(t, notif, 5*time.Second)
	var p struct {
		NodeID string `json:"node_id"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal([]byte(got.Payload), &p); err != nil {
		t.Fatalf("unmarshal payload %q: %v", got.Payload, err)
	}
	if p.NodeID != newID {
		t.Errorf("INSERT payload node_id = %q, want %q", p.NodeID, newID)
	}
	if !p.Active {
		t.Errorf("INSERT payload active = false, want true (column default)")
	}

	// (2) UPDATE — SetComputeNodeActive's drained-row path. The
	// payload's active field must mirror the post-update value, not
	// the pre-update value; gatewayd evicts on either transition but
	// re-arming on active=true depends on the truth coming through.
	if _, err := pool.Exec(ctx,
		`update compute_nodes set active = false where id = $1`, newID,
	); err != nil {
		t.Fatalf("update compute_node active=false: %v", err)
	}
	got = waitForNotification(t, notif, 5*time.Second)
	if err := json.Unmarshal([]byte(got.Payload), &p); err != nil {
		t.Fatalf("unmarshal UPDATE payload %q: %v", got.Payload, err)
	}
	if p.NodeID != newID {
		t.Errorf("UPDATE payload node_id = %q, want %q", p.NodeID, newID)
	}
	if p.Active {
		t.Errorf("UPDATE payload active = true, want false (post-update value)")
	}
}

// waitForNotification blocks up to d for the next entry on the
// notification channel. Failing the test after d elapses is the
// right call here — a missing notification IS the regression, and
// failing fast keeps the test signal clean.
func waitForNotification(t *testing.T, ch <-chan db.Notification, d time.Duration) db.Notification {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(d):
		t.Fatalf("no compute_node_changed notification within %s (trigger missing or channel not LISTENed)", d)
		return db.Notification{}
	}
}
