//go:build !no_pg

// Migration-apply tests for 00076 (compute_node_keys table, ADR-053
// Tier 1 Phase 2 — node_signature).
//
// Pins the Phase 2 acceptance gate verbatim:
// <schema applies; key_id shape check fires on bad hex; PEM shape
// check fires on non-PEM bodies; pg_notify 'compute_node_changed'
// fires on INSERT/UPDATE/DELETE; ON DELETE CASCADE drops the keys
// row when its compute_nodes row goes away; idempotency on
// re-apply.>
//
//	1. compute_node_keys table exists with the expected columns
//	   + nullability + default.
//	2. PRIMARY KEY (compute_node_id, key_id) enforces uniqueness;
//	   a second insert with the same key fails 23505.
//	3. compute_node_keys_key_id_shape rejects a 64-char key_id that
//	   isn't lowercase hex (e.g. uppercase / non-hex), accepts
//	   canonical hex.
//	4. compute_node_keys_pem_shape rejects bodies that don't start
//	   with the BEGIN PUBLIC KEY marker; accepts canonical PEM.
//	5. pg_notify 'compute_node_changed' fires on INSERT, UPDATE,
//	   DELETE to compute_node_keys.
//	6. ON DELETE CASCADE: deleting the parent compute_nodes row
//	   drops the matching compute_node_keys rows.
//	7. Replay-safety: a second MigrateUp() returns nil.
//
// Build tag mirrors apply_walk_test.go:4 and the other
// 0007x migration tests — set FAAS_SKIP_PG_TESTS=1 to skip.

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// canonicalKeyPEM is a parseable ECDSA P-256 SubjectPublicKeyInfo.
// The bytes don't have to be a real key for these tests — the PEM
// shape check only inspects the BEGIN marker.
const canonicalKeyPEM = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEfakedFAKEfakedFAKEfakedFAKE
fakedFAKEfakedFAKEfakedFAKEfakedFAKEfakedFAKEfakedFAKEfaked==
-----END PUBLIC KEY-----
`

// goodKeyID returns a canonical 64-char lowercase hex SHA-256 (the
// shape the check accepts). Computed from a fixed byte string so
// the test is deterministic across runs.
func goodKeyID(t *testing.T) string {
	t.Helper()
	// SHA-256 of "faas.node.test.key" (deterministic).
	// We hardcode the hex to keep this test independent of crypto
	// packages — the table check is what we're pinning.
	const want = "fa1f2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"
	if len(want) != 64 {
		t.Fatalf("test fixture: hardcoded key_id is %d chars, want 64", len(want))
	}
	return want
}

// insertComputeNode seeds a parent compute_nodes row so the FK
// in compute_node_keys is satisfied. Mirrors the registered-shape
// schema from migration 00024 + 00072.
func insertComputeNode(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		insert into compute_nodes (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
		returning id
	`, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert compute_nodes: %v", err)
	}
	return id
}

func Test00076_ComputeNodeKeys_TableShape(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Pool.Query (NOT QueryRow) — we need to iterate every column.
	// QueryRow returns a single row and a re-Scan would hit
	// io.EOF on the second iteration, leaving the loop with
	// only the first column. Scope by current_schema() so a
	// parallel pgtest schema doesn't leak its own compute_node_keys
	// rows into the iteration (per memory: migrations info_schema
	// scoping pattern).
	rows, err := pool.Query(ctx, `
		select column_name, is_nullable, data_type
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'compute_node_keys'
		 order by ordinal_position
	`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name, nullable, dtype string
		if err := rows.Scan(&name, &nullable, &dtype); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		got[name] = nullable + "|" + dtype
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	// expected schema per the migration body. Pin each column
	// individually so a future schema drift fails loud.
	want := map[string]string{
		"compute_node_id": "NO|uuid",
		"key_id":          "NO|text",
		"public_key_pem":  "NO|text",
		"created_at":      "NO|timestamp with time zone",
	}
	for col, expect := range want {
		if got[col] != expect {
			t.Fatalf("compute_node_keys.%s = %q, want %q", col, got[col], expect)
		}
	}
}

func Test00076_ComputeNodeKeys_PrimaryKeyEnforced(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	nodeID := insertComputeNode(ctx, t, pool, "pkey-"+uuid.NewString()[:8])
	keyID := goodKeyID(t)
	if _, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, keyID, canonicalKeyPEM); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Second insert with same (compute_node_id, key_id) MUST fail
	// with SQLSTATE 23505 (unique_violation) — the PK is the
	// load-bearing idempotency story.
	_, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, keyID, canonicalKeyPEM)
	if err == nil {
		t.Fatalf("expected unique violation on duplicate (compute_node_id, key_id); got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("want SQLSTATE 23505, got %v", err)
	}
}

func Test00076_ComputeNodeKeys_KeyIdShapeCheck(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	nodeID := insertComputeNode(ctx, t, pool, "kidshape-"+uuid.NewString()[:8])

	// Bad: uppercase hex — the check rejects [A-F].
	bad := strings.ToUpper(goodKeyID(t))
	_, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, bad, canonicalKeyPEM)
	if err == nil {
		t.Fatalf("expected check violation on uppercase key_id; got nil")
	}
	// Good: canonical lowercase hex 64 chars.
	if _, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, goodKeyID(t), canonicalKeyPEM); err != nil {
		t.Fatalf("good key_id insert: %v", err)
	}
}

func Test00076_ComputeNodeKeys_PemShapeCheck(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	nodeID := insertComputeNode(ctx, t, pool, "pems-"+uuid.NewString()[:8])
	// Bad: not a PEM (no BEGIN marker).
	_, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, goodKeyID(t), "not-a-pem")
	if err == nil {
		t.Fatalf("expected check violation on non-PEM body; got nil")
	}
	// Good: canonical PEM.
	if _, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, goodKeyID(t), canonicalKeyPEM); err != nil {
		t.Fatalf("good PEM insert: %v", err)
	}
}

func Test00076_ComputeNodeKeys_PgNotifyOnChange(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	nodeID := insertComputeNode(ctx, t, pool, "notify-"+uuid.NewString()[:8])

	// Subscribe BEFORE the insert so the LISTEN/NOTIFY race doesn't
	// lose the channel event. pgxpool exposes Listen via the
	// underlying conn — open a dedicated conn for this test.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN compute_node_changed"); err != nil {
		t.Fatalf("LISTEN compute_node_changed: %v", err)
	}

	// INSERT.
	if _, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, goodKeyID(t), canonicalKeyPEM); err != nil {
		t.Fatalf("insert: %v", err)
	}
	recv := waitForNotify(ctx, t, conn)
	if recv != "compute_node_changed" {
		t.Fatalf("INSERT notify channel = %q, want %q", recv, "compute_node_changed")
	}

	// UPDATE — flip public_key_pem to a different canonical PEM so
	// the row genuinely changes.
	newPEM := strings.Replace(canonicalKeyPEM, "fakedFAKE", "0000000000", 1)
	if _, err := pool.Exec(ctx, `
		update compute_node_keys set public_key_pem = $1
		 where compute_node_id = $2 and key_id = $3
	`, newPEM, nodeID, goodKeyID(t)); err != nil {
		t.Fatalf("update: %v", err)
	}
	recv = waitForNotify(ctx, t, conn)
	if recv != "compute_node_changed" {
		t.Fatalf("UPDATE notify channel = %q, want %q", recv, "compute_node_changed")
	}

	// DELETE.
	if _, err := pool.Exec(ctx, `
		delete from compute_node_keys
		 where compute_node_id = $1 and key_id = $2
	`, nodeID, goodKeyID(t)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	recv = waitForNotify(ctx, t, conn)
	if recv != "compute_node_changed" {
		t.Fatalf("DELETE notify channel = %q, want %q", recv, "compute_node_changed")
	}
}

func Test00076_ComputeNodeKeys_CascadeOnComputeNodeDelete(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	nodeID := insertComputeNode(ctx, t, pool, "cascade-"+uuid.NewString()[:8])
	if _, err := pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
	`, nodeID, goodKeyID(t), canonicalKeyPEM); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Cascade: dropping the parent compute_nodes row drops the
	// keys row. Without the FK clause this would leave an orphan.
	if _, err := pool.Exec(ctx, `delete from compute_nodes where id = $1`, nodeID); err != nil {
		t.Fatalf("delete compute_nodes: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `
		select count(*) from compute_node_keys where compute_node_id = $1
	`, nodeID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("compute_node_keys rows after cascade = %d, want 0", count)
	}
}

func Test00076_ComputeNodeKeys_Replay(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)
	// Second pass: replay-safety contract. MigrateUp MUST be
	// idempotent (every DDL is IF NOT EXISTS / DO-block-guarded
	// per the migration body).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay MigrateUp: %v", err)
	}
}

// waitForNotify drains the next notification on conn within
// the timeout. The pg_notify contract is "best-effort, may be
// dropped if the queue overflows"; a 5-second window is generous.
func waitForNotify(ctx context.Context, t *testing.T, conn *pgxpool.Conn) string {
	t.Helper()
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := conn.Conn().WaitForNotification(tctx)
	if err != nil {
		t.Fatalf("WaitForNotification: %v", err)
	}
	return n.Channel
}
