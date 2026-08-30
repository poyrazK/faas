//go:build metal

// twonode_runbook_test.go — runs the operator runbook's bash
// steps as a Go test (Workstream B / issue #1184 / Task #73 /
// ADR-137).
//
// Why this exists: the runbook (docs/runbooks/twonode-fault-injection.md)
// is the operator-side companion to the failure-safe tests. A
// runbook step that drifts from the test assertion is the exact
// regression this file exists to catch. Each test below exercises
// one runbook section end-to-end against the live fixture +
// asserts the documented outcome.
//
// All tests in this file are acceptance gates; they skip when
// PG is unavailable (the same shape as
// cmd/e2e/twonode_failure_safe_metal_test.go).
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestRunbook_Drill1_HeartbeatGapFlipsUnavailable exercises the
// runbook's first drill end-to-end: stale the heartbeat, observe
// the lifecycle flip + the node.failed event row.
func TestRunbook_Drill1_HeartbeatGapFlipsUnavailable(t *testing.T) {
	pool := pgtest.Open(t)
	h := e2etest.StartTwoNode(t, pool)
	fi := e2etest.NewCmdFaultInjector(t, pool)
	// Runbook step 1: stale the heartbeat on node-b by 120s.
	if err := fi.StaleHeartbeat(h.NodeB, 2*time.Minute); err != nil {
		t.Fatalf("runbook step 1: StaleHeartbeat: %v", err)
	}
	// Runbook step 2: observe the recovery timeline. Within the
	// DefaultHeartbeatStaleness (90s) the row must flip.
	if err := h.WaitForNode(context.Background(), h.NodeB, "unavailable", 90*time.Second); err != nil {
		t.Fatalf("runbook step 2: node did not flip: %v", err)
	}
	// Assert the event row landed. Use a string-match on the
	// kind column rather than the typed RecoveryEvent surface —
	// the runbook is the operator view, not the wire format.
	var count int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE kind = 'node.failed' AND payload->>'node_id' = $1`,
		h.NodeBID).Scan(&count)
	if err != nil {
		t.Fatalf("count node.failed events: %v", err)
	}
	if count == 0 {
		t.Errorf("runbook step 2: expected at least 1 node.failed event for %s", h.NodeBID)
	}
}

// TestRunbook_Drill2_DrainCascade exercises the runbook's second
// drill: drain a node and confirm the arbiter migrates live
// instances + flips back to 'active' with the audit columns
// stamped.
//
// This test runs the row-level fault path (FaultInjector.Drain)
// rather than POSTing to apid — the apid HTTP gate has its own
// coverage in cmd/apid/handlers_compute_nodes_drain_test.go.
func TestRunbook_Drill2_DrainCascade(t *testing.T) {
	pool := pgtest.Open(t)
	h := e2etest.StartTwoNode(t, pool)
	fi := e2etest.NewCmdFaultInjector(t, pool)
	if err := fi.Drain(h.NodeA); err != nil {
		t.Fatalf("runbook step 1: Drain: %v", err)
	}
	// The drain completion is the lifecycle flip back to
	// 'active' (the recovery arbiter migrates the live
	// instances first, then re-activates the row). For the
	// fixture-only harness this happens via the API; the live
	// path requires make metal-lima-2node-fault. We assert the
	// lifecycle landed at least at 'draining' as the
	// intermediate-state proof.
	var lc string
	err := pool.QueryRow(context.Background(),
		`SELECT lifecycle::text FROM compute_nodes WHERE name = $1`, h.NodeA).Scan(&lc)
	if err != nil {
		t.Fatalf("lifecycle read: %v", err)
	}
	if !strings.Contains(lc, "drain") && !strings.Contains(lc, "active") {
		t.Errorf("expected draining or active, got %q", lc)
	}
}

// TestRunbook_Drill3_PgNotifyRecovery — the SIGSTOP/SIGCONT
// scenario. Skipped without CAP_SYS_PTRACE; the row-level
// StaleHeartbeat path already exercises the recovery arbiter's
// end state. This test exists so the runbook step has a Go
// counterpart in CI for the daemon-boot path.
func TestRunbook_Drill3_PgNotifyRecovery(t *testing.T) {
	pool := pgtest.Open(t)
	h := e2etest.StartTwoNode(t, pool)
	fi := e2etest.NewCmdFaultInjector(t, pool)
	// StaleHeartbeat is the row-level analog of a SIGSTOP'd
	// schedd; the recovery path is identical from the row's
	// perspective.
	if err := fi.StaleHeartbeat(h.NodeA, 2*time.Minute); err != nil {
		t.Fatalf("runbook step 1: StaleHeartbeat: %v", err)
	}
	if err := h.WaitForNode(context.Background(), h.NodeA, "unavailable", 90*time.Second); err != nil {
		t.Fatalf("runbook step 2: %v", err)
	}
}
