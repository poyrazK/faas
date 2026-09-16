package e2etest

// The synthetic default-local compute_nodes row must point at THIS harness's
// sockets, both of them.
//
// schedd_target_url was repointed; target_url was not. target_url is the VMMD
// endpoint that schedd's heartbeat dials to prove the node is alive. Left at
// the seeded /run/faas/vmmd.sock — a path the native gate guarantees is
// absent, since it stops the production daemons — every heartbeat failed with
// "rpc error: code = Unavailable", the heartbeat gate flipped the row to
// lifecycle='unavailable', and schedd refused every placement:
//
//	claim unplaced: choose: capacity_unavailable: placement:
//	no active compute_node fits 264 MB billable
//
// Builds succeeded and the deployment sat in `building` until the test gave
// up, so it read as a slow build rather than a node marked dead.

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestSetDefaultLocalScheddTarget_RepointsBothEndpoints(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("no Postgres available")
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	const schedd = "/tmp/faas-e2e-sock-probe/schedd.sock"
	const vmmd = "/tmp/faas-e2e-sock-probe/vmmd.sock"
	setDefaultLocalScheddTarget(t, pool, schedd, vmmd)

	var gotSchedd, gotVMMD string
	err := pool.QueryRow(context.Background(),
		`SELECT schedd_target_url, target_url FROM compute_nodes WHERE name = 'default-local'`).
		Scan(&gotSchedd, &gotVMMD)
	if err != nil {
		t.Fatalf("read back default-local: %v", err)
	}

	if want := "unix://" + schedd; gotSchedd != want {
		t.Errorf("schedd_target_url = %q, want %q", gotSchedd, want)
	}
	if want := "unix://" + vmmd; gotVMMD != want {
		t.Errorf("target_url = %q, want %q", gotVMMD, want)
	}
	// The specific regression: anything under the production run directory
	// cannot exist while the gate holds the node.
	if strings.Contains(gotVMMD, "/run/faas/") {
		t.Errorf("target_url = %q still points into the production run directory; "+
			"schedd's heartbeat will fail and the node will be marked unavailable", gotVMMD)
	}
}

// A configuration with no vmmd must leave target_url alone rather than write
// an empty scheme-only URL, which would fail to dial in a way that looks like
// the bug this fixes.
func TestSetDefaultLocalScheddTarget_LeavesTargetAloneWithoutVMMD(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("no Postgres available")
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	var before string
	if err := pool.QueryRow(context.Background(),
		`SELECT target_url FROM compute_nodes WHERE name = 'default-local'`).Scan(&before); err != nil {
		t.Fatalf("read seeded target_url: %v", err)
	}

	setDefaultLocalScheddTarget(t, pool, "/tmp/faas-e2e-sock-probe/schedd.sock", "")

	var after string
	if err := pool.QueryRow(context.Background(),
		`SELECT target_url FROM compute_nodes WHERE name = 'default-local'`).Scan(&after); err != nil {
		t.Fatalf("read back target_url: %v", err)
	}
	if after != before {
		t.Errorf("target_url changed to %q with no vmmd socket; want it left at %q", after, before)
	}
}
