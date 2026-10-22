//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 1027
//
// TestDatabaseVMsConstraints pins the database scheduler's tables: every
// state and bound column is checked, a VM must sit on a known node, and
// last_error only holds stable codes, never free text.
func TestDatabaseVMsConstraints(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	mustExec(t, pool, `INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes) VALUES ('db-1', 'tcp://10.0.2.10:7443', 48000, 3000000000000)`)
	insertVM := `INSERT INTO database_vms (database_id, node_id, postgres_major, vcpu_count, mem_mib, volume_bytes, state, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	mustExec(t, pool, insertVM, uuid.NewString(), "db-1", 17, 1, 384, int64(1<<30), "provisioning", nil)
	// Production class: a replicated database's standby lives on another node.
	mustExec(t, pool, `INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes) VALUES ('db-4', 'tcp://10.0.2.11:7443', 48000, 3000000000000)`)
	insertStandby := `INSERT INTO database_vms (database_id, node_id, postgres_major, vcpu_count, mem_mib, volume_bytes, state, replicated, standby_node, standby_running, standby_synced)
		VALUES ($1, 'db-1', 17, 1, 384, 1073741824, 'running', $2, $3, $4, $5)`
	mustExec(t, pool, insertStandby, uuid.NewString(), true, "db-4", true, true)
	const validVerifier = "SCRAM-SHA-256$4096:c2FsdHNhbHRzYWx0c2FsdA==$c3RvcmVkc3RvcmVkc3RvcmVkc3RvcmVkc3RvcmVkMTI=:c2VydmVyc2VydmVyc2VydmVyc2VydmVyc2VydmVyMTI="
	mustExec(t, pool, `INSERT INTO database_role_verifiers (database_id, role_name, verifier) VALUES ($1, 'app', $2)`, uuid.NewString(), validVerifier)

	rejected := []struct {
		name string
		sql  string
		args []any
		code string
	}{
		{"unknown node", insertVM, []any{uuid.NewString(), "db-9", 17, 1, 384, int64(1 << 30), "parked", nil}, "23503"},
		{"bad state", insertVM, []any{uuid.NewString(), "db-1", 17, 1, 384, int64(1 << 30), "sleeping", nil}, "23514"},
		{"old major", insertVM, []any{uuid.NewString(), "db-1", 12, 1, 384, int64(1 << 30), "parked", nil}, "23514"},
		{"tiny memory", insertVM, []any{uuid.NewString(), "db-1", 17, 1, 64, int64(1 << 30), "parked", nil}, "23514"},
		{"zero volume", insertVM, []any{uuid.NewString(), "db-1", 17, 1, 384, int64(0), "parked", nil}, "23514"},
		{"free-text error", insertVM, []any{uuid.NewString(), "db-1", 17, 1, 384, int64(1 << 30), "failed", "boot failed: /srv/db/x"}, "23514"},
		{"bad node lifecycle", `INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes, lifecycle) VALUES ('db-2', 'tcp://x:1', 1, 1, 'zombie')`, nil, "23514"},
		{"bad node id", `INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes) VALUES ('DB 2', 'tcp://x:1', 1, 1)`, nil, "23514"},
		{"zero ceiling", `INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes) VALUES ('db-3', 'tcp://x:1', 0, 1)`, nil, "23514"},
		{"plaintext password as verifier", `INSERT INTO database_role_verifiers (database_id, role_name, verifier) VALUES ($1, 'app', 'hunter2')`, []any{uuid.NewString()}, "23514"},
		{"bad role name", `INSERT INTO database_role_verifiers (database_id, role_name, verifier) VALUES ($1, 'App; DROP', $2)`, []any{uuid.NewString(), validVerifier}, "23514"},
		{"standby on its own node", insertStandby, []any{uuid.NewString(), true, "db-1", false, false}, "23514"},
		{"standby without replication", insertStandby, []any{uuid.NewString(), false, "db-4", false, false}, "23514"},
		{"running standby without a node", insertStandby, []any{uuid.NewString(), true, nil, true, false}, "23514"},
		{"synced standby without a node", insertStandby, []any{uuid.NewString(), true, nil, false, true}, "23514"},
		{"standby on an unknown node", insertStandby, []any{uuid.NewString(), true, "db-9", false, false}, "23503"},
	}
	for _, tc := range rejected {
		_, err := pool.Exec(ctx, tc.sql, tc.args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
			t.Errorf("%s: err = %v, want SQLSTATE %s", tc.name, err, tc.code)
		}
	}
}
