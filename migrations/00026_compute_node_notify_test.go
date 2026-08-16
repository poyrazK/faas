//go:build !no_pg

// Migration-apply test for 00026 (compute_nodes → compute_node_changed
// pg_notify trigger). Pins the load-bearing contract from issue #98 /
// ADR-028:
//
//   1. The migration set applies cleanly through 00026.
//   2. INSERT into compute_nodes fires compute_node_changed with the
//      new row's id and active=true. gatewayd-internal's per-node client cache
//      eviction hook keys on this payload — a regression that drops
//      the trigger would leave stale conns cached past a node's IP
//      rotation.
//   3. UPDATE on compute_nodes (SetComputeNodeActive path) fires the
//      same channel with the post-update active flag, so the watchdog
//      and heartbeat goroutine paths both surface in gatewayd-internal's cache.
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

	notif, cancel, err := db.Subscribe(ctx, pool, []string{db.NotifyComputeNodesChanged})
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

	got := waitForNodeNotification(t, notif, newID, 5*time.Second)
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
	// the pre-update value; gatewayd-internal evicts on either transition but
	// re-arming on active=true depends on the truth coming through.
	if _, err := pool.Exec(ctx,
		`update compute_nodes set active = false where id = $1`, newID,
	); err != nil {
		t.Fatalf("update compute_node active=false: %v", err)
	}
	got = waitForNodeNotification(t, notif, newID, 5*time.Second)
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

// waitForNodeNotification blocks up to d for the next entry on the
// notification channel whose payload's node_id field equals want.
// pg_notify is cluster-global (LISTEN sees every schema's writes), so a
// parallel pgtest schema's compute_nodes INSERT can leak in here — we
// drop those by JSON-decoding the payload and matching node_id before
// returning. If no matching notification arrives within d, fail the
// test (a missing trigger IS the regression).
func waitForNodeNotification(t *testing.T, ch <-chan db.Notification, want string, d time.Duration) db.Notification {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case n := <-ch:
			var p struct {
				NodeID string `json:"node_id"`
			}
			if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
				// Malformed payloads from sibling schemas can't be ours;
				// drop and keep waiting. A malformed payload from our own
				// trigger would be a real regression and would re-surface
				// in the assertion that called us.
				continue
			}
			if p.NodeID == want {
				return n
			}
		case <-deadline:
			t.Fatalf("no compute_node_changed notification for node_id=%q within %s (trigger missing, channel not LISTENed, or sibling schema's payload flooded the queue)", want, d)
			return db.Notification{}
		}
	}
}
