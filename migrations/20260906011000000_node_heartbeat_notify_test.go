//go:build !no_pg

package migrations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestComputeNodeNotify_HeartbeatKeepsClients(t *testing.T) {
	pool := pgtest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "LISTEN compute_node_changed"); err != nil {
		t.Fatal(err)
	}
	var nodeID string
	err = pool.QueryRow(ctx, `INSERT INTO compute_nodes
		(name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb)
		VALUES ('heartbeat-notify-test', 'unix:///run/test.sock', 4, 4096, 3, 2048)
		RETURNING id`).Scan(&nodeID)
	if err != nil {
		t.Fatal(err)
	}
	// A notification committed after each completed update is a
	// delivery barrier. This checks absence without relying on a short sleep.
	check := func(t *testing.T, want int) {
		t.Helper()
		barrier := nodeID + ":" + t.Name()
		if _, err := pool.Exec(ctx, "SELECT pg_notify('compute_node_changed', $1)", barrier); err != nil {
			t.Fatal(err)
		}
		count := 0
		for {
			n, err := listener.Conn().WaitForNotification(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n.Payload == barrier {
				break
			}
			var p struct {
				NodeID string `json:"node_id"`
			}
			if json.Unmarshal([]byte(n.Payload), &p) == nil && p.NodeID == nodeID {
				count++
			}
		}
		if count != want {
			t.Fatalf("node notifications = %d, want %d", count, want)
		}
	}
	check(t, 1)
	for _, tc := range []struct {
		name string
		sql  string
		want int
	}{
		{"heartbeat", "UPDATE compute_nodes SET last_heartbeat_at = last_heartbeat_at + interval '30 seconds' WHERE id = $1", 0},
		{"unchanged lifecycle", "UPDATE compute_nodes SET lifecycle = lifecycle WHERE id = $1", 0},
		{"target rotation", "UPDATE compute_nodes SET target_url = 'unix:///run/rotated.sock' WHERE id = $1", 1},
		{"schedd rotation", "UPDATE compute_nodes SET schedd_target_url = 'tcp://127.0.0.1:9091' WHERE id = $1", 1},
		{"capacity change", "UPDATE compute_nodes SET mem_mb = mem_mb + 512 WHERE id = $1", 1},
		{"deactivation", "UPDATE compute_nodes SET lifecycle = 'unavailable' WHERE id = $1", 1},
		{"already unavailable", "UPDATE compute_nodes SET lifecycle = 'unavailable' WHERE id = $1", 0},
		{"heartbeat with recovery", "UPDATE compute_nodes SET last_heartbeat_at = now(), lifecycle = 'recovering' WHERE id = $1", 1},
		{"recovery complete", "UPDATE compute_nodes SET lifecycle = 'active' WHERE id = $1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.sql, nodeID); err != nil {
				t.Fatal(err)
			}
			check(t, tc.want)
		})
	}
}
