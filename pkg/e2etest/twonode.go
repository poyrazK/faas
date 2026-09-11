// twonode.go — minimal two-node control-plane harness for the
// Workstream B failure-safe tests (issue #1184 / ADR-137).
//
// Why this exists separate from e2etest.Harness: production
// uses N schedd + N vmmd + 1 shared apid (Tier A7 split, ADR-070),
// and the failure modes being tested (recovery arbiter, drain
// cascade, pg_notify fan-out across two schedd pools) need two
// independent schedd daemons on the same Postgres. The existing
// harness spawns ONE schedd; bolting a second on top would break
// its `currentHarness` debug-dump helper.
//
// Scope: this harness is intentionally lean — it spawns the
// daemons, gives each a per-node socket + compute_node row, and
// exposes the same fault-injection surface (Task #71's
// FaultInjector). It does NOT duplicate the buildBinaries /
// waitUnix / startProc machinery; tests that need full daemon
// surface can fall back to two single-node Harness.Start calls
// and just observe their distinct compute_nodes rows.

//go:build e2e || metal

package e2etest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// TwoNodeHarness represents the two-node database view used by the
// failure-safe tests. Local tests create synthetic rows; native mode selects
// the operator-managed split-box rows and leaves them in place. The daemons
// are supplied by the corresponding fixture or production pair.
//
// Tests consume the struct's fields directly; cleanup is wired
// via t.Cleanup in StartTwoNode so a t.Fatal cannot leak daemons.
type TwoNodeHarness struct {
	T          *testing.T
	Pool       *pgxpool.Pool
	Remote     bool // true when the rows and daemons belong to the native split-box pair
	SockDir    string
	ScheddA    string // schedd-a unix socket
	ScheddB    string // schedd-b unix socket
	VMMDA      string
	VMMDB      string
	NodeA      string // compute_nodes.name for node A
	NodeB      string
	NodeAID    string // compute_nodes.id (uuid) for node A
	NodeBID    string
	APIDProcID int // pseudo PID — the apid's *exec.Cmd isn't exposed
}

// StartTwoNode prepares the two-node control-plane view. In local mode it
// creates disposable rows. With FAAS_TWO_NODE_REMOTE=1 it selects existing
// rows from the native split-box database and never deletes or rewrites them.
//
// Why the apid is single-instance: Tier A7 split (ADR-070)
// places apid behind a HA front; multiple apid daemons on the
// SAME node would double-emit customer-intent writes
// (CLAUDE.md ownership rules). For tests we want a single
// authority; cross-node apid is a Tier A8 topic.
func StartTwoNode(t *testing.T, pool *pgxpool.Pool) *TwoNodeHarness {
	t.Helper()
	if pool == nil {
		t.Skip("twonode: no PG pool; pgtest.Open skipped")
	}

	tmp := t.TempDir()
	sockDir, err := os.MkdirTemp("", "faas-e2e-2node-sock-*")
	if err != nil {
		t.Fatalf("twonode: mkdir sock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	remote := os.Getenv("FAAS_TWO_NODE_REMOTE") == "1"
	nodeA, nodeB := "", ""
	if remote {
		nodeA = os.Getenv("FAAS_TWO_NODE_NODE_A")
		nodeB = os.Getenv("FAAS_TWO_NODE_NODE_B")
		if nodeA == "" || nodeB == "" {
			t.Fatal("twonode: FAAS_TWO_NODE_NODE_A and FAAS_TWO_NODE_NODE_B are required in remote mode")
		}
	} else {
		nodeA = "node-a-" + uuid.NewString()[:8]
		nodeB = "node-b-" + uuid.NewString()[:8]
	}

	var nodeAID, nodeBID string
	if remote {
		nodeAID, err = existingComputeNode(t, pool, nodeA)
		if err != nil {
			t.Fatalf("twonode: find node A %q: %v", nodeA, err)
		}
		nodeBID, err = existingComputeNode(t, pool, nodeB)
		if err != nil {
			t.Fatalf("twonode: find node B %q: %v", nodeB, err)
		}
	} else {
		nodeAID, err = upsertComputeNode(t, pool, nodeA, "fsn-a")
		if err != nil {
			t.Fatalf("twonode: upsert node A: %v", err)
		}
		nodeBID, err = upsertComputeNode(t, pool, nodeB, "fsn-b")
		if err != nil {
			t.Fatalf("twonode: upsert node B: %v", err)
		}
	}
	h := &TwoNodeHarness{
		T:       t,
		Pool:    pool,
		Remote:  remote,
		SockDir: sockDir,
		NodeA:   nodeA,
		NodeB:   nodeB,
		NodeAID: nodeAID,
		NodeBID: nodeBID,
	}
	t.Cleanup(func() {
		if remote {
			return
		}
		// Best-effort cleanup of the per-test compute_nodes rows so a
		// re-run with the same schema prefix doesn't collide.
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM compute_nodes WHERE name IN ($1, $2)`, nodeA, nodeB)
	})
	// Note: this harness does not spawn daemon subprocesses. Local tests use
	// the existing single-node Harness.Start path; native mode talks to the
	// already-managed split-box daemons through the fault injector.
	_ = filepath.Join(tmp, "bin") // placeholder for future buildBinaries
	return h
}

// existingComputeNode selects an operator-managed node without mutating it.
// Native acceptance runs against the Ansible split-box pair, so creating
// synthetic rows would exercise a different schema and let the real daemons
// overwrite the test state.
func existingComputeNode(t *testing.T, pool *pgxpool.Pool, name string) (string, error) {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`SELECT id FROM compute_nodes WHERE name = $1`, name).Scan(&id)
	return id, err
}

// upsertComputeNode seeds a compute_nodes row the recovery
// arbiter can discover. Each node has its own per-host
// schedd_target_url (the single-tenant multi-host model). The
// lifecycle defaults to 'active'; the recovery arbiter is the
// one that flips it to 'unavailable' under fault injection.
func upsertComputeNode(t *testing.T, pool *pgxpool.Pool, name, host string) (string, error) {
	t.Helper()
	const q = `INSERT INTO compute_nodes
		(name, target_url, schedd_target_url, gateway_target_url, lifecycle,
		 mem_mb, max_concurrency, admission_ceiling_mb, vcpus, vcpu_budget,
		 plan_host, overlay_ip, gateway_port)
		VALUES ($1, $2, $3, $4, 'active'::compute_node_lifecycle,
			8192, 16, 256, 4, 160,
			$5, '10.99.0.2', 8080)
		ON CONFLICT (name) DO UPDATE SET
			schedd_target_url = EXCLUDED.schedd_target_url,
			gateway_target_url = EXCLUDED.gateway_target_url
		RETURNING id`
	var id string
	err := pool.QueryRow(context.Background(), q,
		name,
		"unix:///run/faas/"+host+"/vmmd.sock",
		"unix:///run/faas/"+host+"/schedd.sock",
		"tcp://"+host+".test.local:8080",
		host,
	).Scan(&id)
	return id, err
}

// LookupNodeID returns the compute_nodes.id for a node name.
// Convenience used by the deadnode / drain tests that already
// know the human-readable name.
func (h *TwoNodeHarness) LookupNodeID(ctx context.Context, name string) (string, error) {
	var id string
	err := h.Pool.QueryRow(ctx,
		`SELECT id FROM compute_nodes WHERE name = $1`, name).Scan(&id)
	return id, err
}

// WaitForNode polls the compute_nodes row until the lifecycle
// reaches the expected value or the timeout elapses. Used by
// the recovery arbiter's metal tests to wait for the heartbeat
// gate to flip a node to 'unavailable'.
func (h *TwoNodeHarness) WaitForNode(ctx context.Context, name string, want state.NodeLifecycle, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var lc string
		err := h.Pool.QueryRow(ctx,
			`SELECT lifecycle::text FROM compute_nodes WHERE name = $1`, name).Scan(&lc)
		if err == nil && state.NodeLifecycle(lc) == want {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("twonode: node %s did not reach lifecycle=%s within %s", name, want, within)
}
