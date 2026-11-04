// adr: 20261026195952
package mcpmanagement

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func openStatePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Open(t)
	raw, err := os.ReadFile("../../migrations/20261026200448811_mcp_bridge_state.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := pgtest.ExecMigration(t.Context(), t, pool, string(raw), true); err != nil {
		t.Fatal(err)
	}
	return pool
}

func writeStateKey(t *testing.T, fill byte) string {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	path := filepath.Join(t.TempDir(), "state.key")
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func openPGState(t *testing.T, pool *pgxpool.Pool, key string, mutate ...func(*PostgresStateOptions)) (*StateStore, error) {
	t.Helper()
	opts := PostgresStateOptions{Pool: pool, Bridge: "hosted", KeyPath: key, LeaseTTL: 3 * time.Second, AcquireTimeout: time.Second}
	for _, m := range mutate {
		m(&opts)
	}
	return OpenPostgresStateStore(t.Context(), opts)
}

func TestPostgresStateRoundTripsSealedNamespaces(t *testing.T) {
	pool := openStatePool(t)
	key := writeStateKey(t, 7)
	store, err := openPGState(t, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := oauthSnapshot{Grants: map[string]storedGrant{"g1": {}}}
	if err := store.update("oauth", snapshot); err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"plans/g1", "plans/g2"} {
		if err := store.update(ns, map[string]string{"plan": ns}); err != nil {
			t.Fatal(err)
		}
	}
	// Revoking g2 (absent from the snapshot) deletes its plan row too.
	if err := store.update("oauth", snapshot); err != nil {
		t.Fatal(err)
	}
	var plaintext int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM mcp_bridge_state WHERE position(convert_to('plans/g1', 'UTF8') in sealed) > 0`).Scan(&plaintext); err != nil || plaintext != 0 {
		t.Fatalf("plaintext in sealed rows: %d %v", plaintext, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := openPGState(t, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	var plan map[string]string
	if err := restored.read("plans/g1", &plan); err != nil || plan["plan"] != "plans/g1" {
		t.Fatalf("plans/g1 = %v %v", plan, err)
	}
	var gone map[string]string
	if err := restored.read("plans/g2", &gone); err != nil || gone != nil {
		t.Fatalf("revoked grant's plans survived: %v %v", gone, err)
	}
	var epoch int64
	if err := pool.QueryRow(t.Context(), `SELECT epoch FROM mcp_bridge_leases WHERE bridge = 'hosted'`).Scan(&epoch); err != nil || epoch != 2 {
		t.Fatalf("epoch after reopen = %d %v", epoch, err)
	}
}

func TestPostgresStateRefusesWrongKeyAndSwappedRows(t *testing.T) {
	pool := openStatePool(t)
	key := writeStateKey(t, 7)
	store, err := openPGState(t, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.update("plans/a", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("plans/b", map[string]int{"n": 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openPGState(t, pool, writeStateKey(t, 9)); err == nil {
		t.Fatal("a different key opened the state")
	}
	// A row moved to another namespace no longer authenticates.
	if _, err := pool.Exec(t.Context(), `UPDATE mcp_bridge_state SET sealed = (SELECT sealed FROM mcp_bridge_state WHERE namespace = 'plans/a') WHERE namespace = 'plans/b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := openPGState(t, pool, key); err == nil {
		t.Fatal("a swapped row authenticated")
	}
}

func TestPostgresStateDiscardsWhatANewKeyCannotOpen(t *testing.T) {
	pool := openStatePool(t)
	store, err := openPGState(t, pool, writeStateKey(t, 7))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.update("plans/a", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var discarded error
	fresh, err := openPGState(t, pool, writeStateKey(t, 9), func(o *PostgresStateOptions) {
		o.DiscardUnreadable, o.OnDiscard = true, func(err error) { discarded = err }
	})
	if err != nil || discarded == nil {
		t.Fatalf("open with a new key: %v, discarded=%v", err, discarded)
	}
	defer func() { _ = fresh.Close() }()
	var plan map[string]int
	if err := fresh.read("plans/a", &plan); err != nil || plan != nil {
		t.Fatalf("unreadable state survived: %v %v", plan, err)
	}
	var rows int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM mcp_bridge_state`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows left = %d %v", rows, err)
	}
	if err := fresh.update("plans/b", map[string]int{"n": 2}); err != nil {
		t.Fatalf("the new key cannot write: %v", err)
	}
}

// A write that cannot commit leaves memory ahead of Postgres, so the store
// disables itself and asks the process to exit once; a restart reloads.
func TestPostgresStateCommitFailureStopsTheBridgeOnce(t *testing.T) {
	pool := openStatePool(t)
	key := writeStateKey(t, 7)
	failures := make(chan error, 2)
	store, err := openPGState(t, pool, key, func(o *PostgresStateOptions) { o.OnFailure = func(err error) { failures <- err } })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := pool.Exec(t.Context(), `ALTER TABLE mcp_bridge_state RENAME TO mcp_bridge_state_gone`); err != nil {
		t.Fatal(err)
	}
	if err := store.update("plans/g", map[string]int{"n": 1}); err == nil || store.available() {
		t.Fatal("a failed commit left the store open")
	}
	if err := store.update("plans/g", map[string]int{"n": 2}); err == nil {
		t.Fatal("a disabled store accepted a write")
	}
	select {
	case err := <-failures:
		if !errors.Is(err, ErrStateCommitFailed) || errors.Is(err, ErrStateLeaseLost) {
			t.Fatalf("failure = %v, want a commit failure", err)
		}
	default:
		t.Fatal("the commit failure was not reported")
	}
	if len(failures) != 0 {
		t.Fatal("the failure was reported twice")
	}
}

func TestPostgresStateLeaseFencesAFormerHolder(t *testing.T) {
	pool := openStatePool(t)
	key := writeStateKey(t, 7)
	lost := make(chan error, 1)
	first, err := openPGState(t, pool, key, func(o *PostgresStateOptions) { o.OnFailure = func(err error) { lost <- err } })
	if err != nil {
		t.Fatal(err)
	}
	if err := first.update("oauth", oauthSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openPGState(t, pool, key); err == nil {
		t.Fatal("a second bridge took a live lease")
	}

	// The first holder stalls past its lease: a successor takes over.
	if _, err := pool.Exec(t.Context(), `UPDATE mcp_bridge_leases SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	second, err := openPGState(t, pool, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if err := second.update("plans/g", map[string]string{"owner": "second"}); err != nil {
		t.Fatal(err)
	}
	if err := first.update("plans/g", map[string]string{"owner": "first"}); err == nil || first.available() {
		t.Fatal("the former holder still wrote")
	}
	select {
	case err := <-lost:
		if !errors.Is(err, ErrStateLeaseLost) {
			t.Fatalf("lease loss = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the former holder never noticed it lost the lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var owner string
	if err := pool.QueryRow(ctx, `SELECT epoch::text FROM mcp_bridge_state WHERE namespace = 'plans/g'`).Scan(&owner); err != nil || owner != "2" {
		t.Fatalf("plans/g written at epoch %s, %v", owner, err)
	}
	var plan map[string]string
	if err := second.read("plans/g", &plan); err != nil || plan["owner"] != "second" {
		t.Fatalf("successor's state = %v %v", plan, err)
	}
	_ = first.Close()
}
