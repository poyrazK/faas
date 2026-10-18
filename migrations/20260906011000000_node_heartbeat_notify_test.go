//go:build !no_pg

package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
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
	update := func(sql string) func() error {
		return func() error {
			_, err := pool.Exec(ctx, sql, nodeID)
			return err
		}
	}
	// recovering -> active is a fenced completion since ADR-1527: the guard
	// rejects a bare lifecycle UPDATE without a generation-bound receipt, so
	// finish recovery the way the recovery runner does.
	store := state.NewPgStore(pool)
	completeRecovery := func() error {
		node, err := store.NodeGet(ctx, nodeID)
		if err != nil {
			return err
		}
		_, err = store.CompleteNodeLifecycle(ctx, state.NodeLifecycleCompletion{
			NodeID: nodeID, Generation: node.LifecycleGeneration, Lifecycle: node.Lifecycle,
		})
		return err
	}
	for _, tc := range []struct {
		name string
		run  func() error
		want int
	}{
		{"heartbeat", update("UPDATE compute_nodes SET last_heartbeat_at = last_heartbeat_at + interval '30 seconds' WHERE id = $1"), 0},
		{"unchanged lifecycle", update("UPDATE compute_nodes SET lifecycle = lifecycle WHERE id = $1"), 0},
		{"target rotation", update("UPDATE compute_nodes SET target_url = 'unix:///run/rotated.sock' WHERE id = $1"), 1},
		{"schedd rotation", update("UPDATE compute_nodes SET schedd_target_url = 'tcp://127.0.0.1:9091' WHERE id = $1"), 1},
		{"capacity change", update("UPDATE compute_nodes SET mem_mb = mem_mb + 512 WHERE id = $1"), 1},
		{"deactivation", update("UPDATE compute_nodes SET lifecycle = 'unavailable' WHERE id = $1"), 1},
		{"already unavailable", update("UPDATE compute_nodes SET lifecycle = 'unavailable' WHERE id = $1"), 0},
		{"heartbeat with recovery", update("UPDATE compute_nodes SET last_heartbeat_at = now(), lifecycle = 'recovering' WHERE id = $1"), 1},
		{"recovery complete", completeRecovery, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			check(t, tc.want)
		})
	}
	// Execute the actual down/up SQL against the migrated schema to ensure
	// rollback restores the old contract and reapplication is safe.
	source, err := migrations.FS.ReadFile("20260906011000000_node_heartbeat_notify.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	for _, tc := range []struct {
		name string
		sql  string
		want int
	}{{"rollback", down, 1}, {"reapply", up, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, "UPDATE compute_nodes SET last_heartbeat_at = now() WHERE id = $1", nodeID); err != nil {
				t.Fatal(err)
			}
			check(t, tc.want)
		})
	}
}
