//go:build metal

// adr: 468
package e2e_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// All regional fault drills qualify every worker before creating workloads or
// changing services. Duplicate SSH aliases cannot stand in for distinct hosts.
func regionalAcceptanceWorkers(t *testing.T, ctx context.Context, store *state.PgStore, required func(string) string) ([]state.ComputeNode, []string) {
	t.Helper()
	nodes := make([]state.ComputeNode, 3)
	targets := make([]string, 3)
	machines := make(map[string]bool)
	for i, suffix := range []string{"A", "PEER", "B"} {
		var err error
		nodes[i], err = store.ComputeNodeByName(ctx, required("FAAS_REGION_NODE_"+suffix))
		if err != nil || !state.ComputeNodePlacementEligible(nodes[i]) || nodes[i].VPCPUs <= 0 {
			t.Fatalf("worker must be eligible with physical CPU capacity: %v", err)
		}
		targets[i] = required("FAAS_REGION_SSH_" + suffix)
		run, err := e2etest.NativeAcceptanceSSH(targets[i])
		if err != nil {
			t.Fatal(err)
		}
		host, err := e2etest.ProbeNativeAcceptanceHost(ctx, run)
		if err != nil {
			t.Fatalf("worker %s failed native preflight: %v", nodes[i].Name, err)
		}
		if machines[host.MachineID] {
			t.Fatal("each regional worker must run on a distinct native host")
		}
		machines[host.MachineID] = true
		if _, err := run(ctx, "sudo", "-n", "systemctl", "is-active", "--quiet", "faas-schedd", "faas-vmmd"); err != nil {
			t.Fatalf("worker %s needs an active schedd and vmmd: %v", nodes[i].Name, err)
		}
		t.Logf("native regional worker %s: machine=%s platform=%s virtualization=%s", nodes[i].Name, host.MachineID, host.Platform, host.Virtualization)
	}
	if nodes[0].ID == nodes[1].ID || nodes[0].ID == nodes[2].ID || nodes[1].ID == nodes[2].ID ||
		nodes[0].Region == nil || nodes[1].Region == nil || nodes[2].Region == nil ||
		*nodes[0].Region != *nodes[1].Region || *nodes[0].Region == *nodes[2].Region {
		t.Fatal("need two distinct regional workers and a third in another region")
	}
	all, err := store.ListAllComputeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, node := range all {
		if state.ComputeRegionAllowsNode(*nodes[0].Region, node) {
			count++
		}
	}
	if count != 2 {
		t.Fatal("selected acceptance region must contain exactly the designated two workers")
	}
	return nodes, targets
}
