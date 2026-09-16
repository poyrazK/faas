package e2etest

// Schema conformance for the two-node fixture's compute_nodes seed statement.
//
// The statement used to live in twonode.go (`//go:build e2e || metal`), which
// nothing in CI builds — `make e2e` passes no tags and only `make test-metal`
// sets metal, which needs KVM and root. So it was never compiled by an
// ordinary test run, and four wrong column names accumulated in it: `vcpus`
// (the real column is `vpcpus`; migration 00024 transposed the letters and
// nothing renamed it) plus `plan_host`, `overlay_ip` and `gateway_port`,
// which are not columns of compute_nodes at all. The first native-hardware
// run failed all six two-node tests with
//
//	twonode: upsert node A: ERROR: column "vcpus" of relation
//	  "compute_nodes" does not exist (SQLSTATE 42703)
//
// one column at a time. These tests are deliberately UNTAGGED: a fixture that
// only runs on hardware is a fixture whose bugs are found on hardware.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// seedNode executes the real fixture statement, the same way upsertComputeNode
// does, so this test fails on exactly the drift that broke the native gate.
func seedNode(t *testing.T, pool *pgxpool.Pool, name, host string) (string, error) {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), upsertComputeNodeSQL,
		name,
		"unix:///run/faas/"+host+"/vmmd.sock",
		"unix:///run/faas/"+host+"/schedd.sock",
		"tcp://"+host+".test.local:8080",
	).Scan(&id)
	return id, err
}

func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("no Postgres available")
	}
	// OpenMigrated only clones a migrated template when UseTemplateDatabase
	// is set; otherwise it falls back to plain Open and the schema is empty.
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	return pool
}

func TestUpsertComputeNodeSQL_MatchesTheMigratedSchema(t *testing.T) {
	pool := migratedPool(t)

	id, err := seedNode(t, pool, "node-a", "host-a")
	if err != nil {
		t.Fatalf("seed compute node: %v", err)
	}
	if id == "" {
		t.Fatal("seed returned an empty id")
	}

	// Re-running must take the ON CONFLICT branch and resolve to the same
	// row, which the two-node harness relies on across daemon restarts.
	again, err := seedNode(t, pool, "node-a", "host-a")
	if err != nil {
		t.Fatalf("seed compute node (second call): %v", err)
	}
	if again != id {
		t.Errorf("seed returned %q then %q; the conflict branch should "+
			"resolve to one row", id, again)
	}
}

// Inserting successfully is not enough: the row must be discoverable the way
// the recovery arbiter finds it, with an active lifecycle and a per-host
// schedd target. A row that inserts but carries wrong values would satisfy
// the statement-level test above while leaving the two-node tests red for a
// subtler reason.
func TestUpsertComputeNodeSQL_SeedsADiscoverableActiveNode(t *testing.T) {
	pool := migratedPool(t)

	if _, err := seedNode(t, pool, "node-b", "host-b"); err != nil {
		t.Fatalf("seed compute node: %v", err)
	}

	var lifecycle, scheddTarget string
	var vpcpus, vcpuBudget int
	err := pool.QueryRow(context.Background(),
		`SELECT lifecycle::text, schedd_target_url, vpcpus, vcpu_budget
		   FROM compute_nodes WHERE name = $1`, "node-b").
		Scan(&lifecycle, &scheddTarget, &vpcpus, &vcpuBudget)
	if err != nil {
		t.Fatalf("read back seeded node: %v", err)
	}

	if lifecycle != "active" {
		t.Errorf("lifecycle = %q, want active; the recovery arbiter only "+
			"discovers active nodes", lifecycle)
	}
	if want := "unix:///run/faas/host-b/schedd.sock"; scheddTarget != want {
		t.Errorf("schedd_target_url = %q, want %q", scheddTarget, want)
	}
	// Both columns carry NOT NULL CHECK (> 0) constraints, so a zero here
	// means the insert stopped populating them.
	if vpcpus <= 0 {
		t.Errorf("vpcpus = %d, want > 0", vpcpus)
	}
	if vcpuBudget <= 0 {
		t.Errorf("vcpu_budget = %d, want > 0", vcpuBudget)
	}
}
