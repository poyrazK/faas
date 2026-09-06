//go:build !no_pg

// WatchNodeChanges end-to-end pin against a live Postgres.
// Multi-host safety cluster PR-6 (audit F3 verify) — the
// production wiring at scheddrouter.go:288-317 subscribes to
// `compute_node_changed` via db.SubscribeWithReconnect and
// routes every payload through r.Evict. Without a live PG pin,
// a regression in:
//
//   - the trigger payload shape (e.g. {id} → {node_id} rename
//     in pkg/db/notify.go:248-250 or its caller),
//   - the channel name constant (db.NotifyComputeNodeChanged),
//   - or the LISTEN-side decoding (a future refactor that swaps
//     the channel for a struct-backed listener),
//
// would break every gatewayd-internal in the fleet silently —
// cached clients would survive the death of their schedd until
// the next dial, and traffic would route to a dead box. The
// unit test in scheddrouter_test.go (TestScheddRouter_WatchNodeChanges_*)
// pins the seam WITHOUT real LISTEN; THIS test pins it WITH
// real LISTEN. Both layers matter.
//
// The test runs against a fresh schema per pgtest.Open (see
// pkg/db/pgtest/pgtest.go) and migrates via db.MigrateUp. The
// `compute_nodes` table is a goose-managed migration (00079+)
// so we don't have to create it by hand. A real compute_node
// row is seeded with active=true; we then UPDATE active=false
// which fires the existing `compute_node_changed_trg` trigger
// (defined in migrations/00079_compute_nodes_active.sql). The
// trigger emits the {node_id, active} payload the router
// unmarshals. Asserting r.Evict fired proves the full chain
// works.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestScheddRouter_WatchNodeChanges_LiveNotify pins the full
// pg_notify → LISTEN → Evict chain end-to-end. Companion to the
// fake-subscribeFunc tests in scheddrouter_test.go: a future
// regression that touches the wire shape (channel name, payload
// field rename, trigger definition) must fail this test.
func TestScheddRouter_WatchNodeChanges_LiveNotify(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed a compute_node row the router can cache. The router
	// only knows the cache by node_id; the ScheddTargetURL must
	// be non-nil because dialer may otherwise refuse to dial.
	// Use a fake address — we never actually dial it; the test
	// asserts Evict fired by inspecting r.cache.
	url := "tcp://10.0.0.1:7100"
	nodeID := seedComputeNode(t, pool, url)

	store := &stubRouterStore{
		nodes: map[string]state.ComputeNode{
			nodeID: {ID: nodeID, Name: "fsn-test", Active: true, ScheddTargetURL: &url},
		},
	}
	dial := newFakeScheddDial()

	r := newScheddRouter(store, nil, dial.Dial, nil)
	defer func() { _ = r.Close() }()

	// Pre-populate the cache. After the trigger fires, this
	// entry should disappear from r.cache.
	_, err := r.ScheddForApp(ctx, state.App{ID: "app-1", NodeID: nodeID})
	if err != nil {
		t.Fatalf("ScheddForApp: %v", err)
	}

	// Start WatchNodeChanges against the real pool. The function
	// blocks; we run it in a goroutine and cancel at the end.
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.WatchNodeChanges(watchCtx, pool, nil)
	}()

	// Give the LISTEN a moment to register. db.SubscribeWithReconnect
	// performs the first Subscribe synchronously (notify.go:444), but
	// there's a tiny window between the channel creation and the
	// LISTEN ack landing at the server. 100ms is generous on every
	// CI box we've measured.
	time.Sleep(100 * time.Millisecond)

	// Flip lifecycle to unavailable. The compatibility generated
	// active column becomes false and the trigger emits
	// `{node_id, active}` on compute_node_changed.
	if _, err := pool.Exec(ctx,
		`update compute_nodes set lifecycle = 'unavailable'::compute_node_lifecycle where id = $1`, nodeID); err != nil {
		t.Fatalf("update compute_nodes: %v", err)
	}

	// Poll for eviction. pg_notify's default lag is sub-millisecond
	// on a healthy local PG; 2s covers CI scheduling jitter and
	// busy CI hosts. We must not Assert within 2s — the channel
	// delivery is asynchronous.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		_, stillCached := r.cache[nodeID]
		r.mu.Unlock()
		if !stillCached {
			cancel()
			<-done
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	r.mu.Lock()
	_, stillCached := r.cache[nodeID]
	r.mu.Unlock()
	if stillCached {
		t.Fatal("LiveNotify: cache still has node_id 2s after compute_nodes.active=false UPDATE — pg_notify → LISTEN → Evict chain is broken")
	}
}

// seedComputeNode inserts a minimal compute_node row that the
// router can resolve. Returns the generated UUID. The router
// only reads .ID + .ScheddTargetURL + .Active; we set the rest
// to schema defaults (lifecycle=active).
func seedComputeNode(t *testing.T, pool *pgxpool.Pool, targetURL string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`insert into compute_nodes
		 (name, target_url, schedd_target_url, lifecycle, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb)
		 values ($1, 'unix:///run/test-vmmd.sock', $2, 'active'::compute_node_lifecycle, 4, 4096, 3, 2048)
		 returning id`, "fsn-live-test", targetURL).Scan(&id)
	if err != nil {
		t.Fatalf("seed compute_node: %v", err)
	}
	return id
}
