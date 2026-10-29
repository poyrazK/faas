// adr: 20261024225612 — CPU-compatible snapshot restore and per-platform
// snapshot slots (amendment 1).
package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	cascadeSig = "cascadelake/GenuineIntel-6-85-7/aaaaaaaaaaaa"
	iceSig     = "icelake/GenuineIntel-6-106-6/bbbbbbbbbbbb"
	rocketSig  = "unknown/GenuineIntel-6-167-1/cccccccccccc"
	milanSig   = "milan/AuthenticAMD-25-1-1/dddddddddddd"
)

func cpuNode(template, model string) state.ComputeNode {
	return state.ComputeNode{CPUTemplate: template, CPUModel: model}
}

func TestSnapshotRestorableOnRules(t *testing.T) {
	snap := func(template, model string) state.Snapshot {
		return state.Snapshot{CPUTemplate: template, CPUModel: model}
	}
	cases := []struct {
		name     string
		snap     state.Snapshot
		node     state.ComputeNode
		legacyOK bool
		want     bool
	}{
		{"T2CL capture on a T2CL Cascade Lake node", snap("T2CL", iceSig), cpuNode("T2CL", cascadeSig), true, true},
		{"T2CL capture on a template-less Ice Lake node", snap("T2CL", cascadeSig), cpuNode("", iceSig), true, true},
		{"T2CL capture on Rocket Lake", snap("T2CL", cascadeSig), cpuNode("", rocketSig), true, false},
		{"T2CL capture on AMD Milan", snap("T2CL", cascadeSig), cpuNode("", milanSig), true, false},
		{"T2CL capture on a node that reports nothing", snap("T2CL", cascadeSig), cpuNode("", ""), true, false},
		{"template-less capture on the identical host", snap("", iceSig), cpuNode("T2CL", iceSig), true, true},
		{"template-less Ice Lake capture on Cascade Lake", snap("", iceSig), cpuNode("", cascadeSig), true, false},
		{"legacy row on a template-less node", snap("", ""), cpuNode("", cascadeSig), true, true},
		{"legacy row on an unreported node", snap("", ""), cpuNode("", ""), true, true},
		{"legacy row on a templated node", snap("", ""), cpuNode("T2CL", cascadeSig), true, false},
		{"legacy row in a mixed template-less fleet", snap("", ""), cpuNode("", cascadeSig), false, false},
	}
	for _, tc := range cases {
		if got := sched.SnapshotRestorableOn(tc.snap, tc.node, tc.legacyOK); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if !sched.LegacySnapshotsRestorable([]state.ComputeNode{cpuNode("", cascadeSig), cpuNode("", cascadeSig), cpuNode("", ""), cpuNode("T2CL", iceSig)}) {
		t.Error("one template-less model plus unreported and templated nodes must keep legacy rows restorable")
	}
	if sched.LegacySnapshotsRestorable([]state.ComputeNode{cpuNode("", cascadeSig), cpuNode("", iceSig)}) {
		t.Error("two template-less models must retire legacy restores")
	}
}

func TestCPUIdentityWarning(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes []state.ComputeNode
		warn  bool
	}{
		{"one T2CL cell across Cascade and Ice Lake", []state.ComputeNode{cpuNode("T2CL", cascadeSig), cpuNode("T2CL", iceSig)}, false},
		{"identical template-less nodes", []state.ComputeNode{cpuNode("", cascadeSig), cpuNode("", cascadeSig)}, false},
		{"unreported nodes are not counted", []state.ComputeNode{cpuNode("", ""), cpuNode("", cascadeSig)}, false},
		{"mixed templates", []state.ComputeNode{cpuNode("T2CL", cascadeSig), cpuNode("", cascadeSig)}, true},
		{"template-less nodes on different CPUs", []state.ComputeNode{cpuNode("", cascadeSig), cpuNode("", iceSig)}, true},
	} {
		if _, warning := sched.CPUIdentityWarning(tc.nodes); (warning != "") != tc.warn {
			t.Errorf("%s: warning = %q", tc.name, warning)
		}
	}
}

// setNodeCPU registers a node's CPU identity the way vmmd does.
func setNodeCPU(t *testing.T, store state.Store, nodeID, template, model string, ceilingMB int) {
	t.Helper()
	ctx := context.Background()
	node, err := store.ComputeNodeByID(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	node.CPUTemplate, node.CPUModel = template, model
	if ceilingMB > 0 {
		node.AdmissionCeilingMB = ceilingMB
	}
	if _, err := store.UpsertComputeNode(ctx, node); err != nil {
		t.Fatal(err)
	}
}

// createCPUSnapshot stores a live init-tier capture of dep from a node with
// the given CPU identity.
func createCPUSnapshot(t *testing.T, store *state.MemStore, depID, name, template, model string, at time.Time) {
	t.Helper()
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: depID, FCVersion: "1.10.0", Tier: state.SnapshotTierInit, CreatedAt: at,
		StorageKey:  state.SnapshotCaptureMemKey(depID, state.SnapshotTierInit, name),
		CPUTemplate: template, CPUModel: model,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWakeRestoresOnlyOnCPUCompatibleNodes(t *testing.T) {
	type nodeCPU struct {
		template, model string
		ceilingMB       int
		full            bool
	}
	for _, tc := range []struct {
		name         string
		snapTemplate string
		snapModel    string
		a, b         nodeCPU
		wantRestore  bool
		wantNode     string // "a" or "b"
	}{
		{
			name:         "T2CL capture skips the roomier Rocket Lake node",
			snapTemplate: "T2CL", snapModel: iceSig,
			a: nodeCPU{"T2CL", cascadeSig, 4096, false}, b: nodeCPU{"", rocketSig, 0, false},
			wantRestore: true, wantNode: "a",
		},
		{
			name:         "template-less capture finds its identical host",
			snapTemplate: "", snapModel: iceSig,
			a: nodeCPU{"", cascadeSig, 0, false}, b: nodeCPU{"", iceSig, 4096, false},
			wantRestore: true, wantNode: "b",
		},
		{
			name:         "no compatible node cold boots",
			snapTemplate: "T2CL", snapModel: cascadeSig,
			a: nodeCPU{"", rocketSig, 0, false}, b: nodeCPU{"", milanSig, 0, false},
			wantRestore: false,
		},
		{
			name:         "compatible node full cold boots on the other",
			snapTemplate: "T2CL", snapModel: cascadeSig,
			a: nodeCPU{"T2CL", iceSig, 1024, true}, b: nodeCPU{"", milanSig, 0, false},
			wantRestore: false, wantNode: "b",
		},
		{
			name:         "legacy row in a mixed template-less fleet cold boots",
			snapTemplate: "", snapModel: "",
			a: nodeCPU{"", cascadeSig, 0, false}, b: nodeCPU{"", iceSig, 0, false},
			wantRestore: false,
		},
		{
			name:         "legacy row in a T2CL cell cold boots once",
			snapTemplate: "", snapModel: "",
			a: nodeCPU{"T2CL", cascadeSig, 0, false}, b: nodeCPU{"T2CL", iceSig, 0, false},
			wantRestore: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			a, b := sched.SeedTwoNodes(t, store)
			setNodeCPU(t, store, a, tc.a.template, tc.a.model, tc.a.ceilingMB)
			setNodeCPU(t, store, b, tc.b.template, tc.b.model, tc.b.ceilingMB)
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
			createCPUSnapshot(t, store, dep.ID, "cpu", tc.snapTemplate, tc.snapModel, time.Time{})
			vmm := &sched.FakeVMM{}
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			if tc.a.full {
				(*e.XCapacityTable()).Replace(sched.CapacityReport{NodeID: a, UsedMB: int32(tc.a.ceilingMB)})
			}
			got, err := e.Wake(ctx, app.ID, "", "", "gateway")
			if err != nil {
				t.Fatal(err)
			}
			restores, coldBoots := *vmm.Restores(), *vmm.ColdBoots()
			if tc.wantRestore && (restores != 1 || coldBoots != 0) {
				t.Fatalf("restores=%d cold boots=%d, want one restore", restores, coldBoots)
			}
			if !tc.wantRestore && (restores != 0 || coldBoots != 1) {
				t.Fatalf("restores=%d cold boots=%d, want one cold boot", restores, coldBoots)
			}
			if want := map[string]string{"a": a, "b": b}[tc.wantNode]; want != "" && got.NodeID != want {
				t.Fatalf("wake node = %s, want %s", got.NodeID, want)
			}
		})
	}
}

func TestSnapshotWrittenCarriesCapturingNodeCPU(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	a, _ := sched.SeedTwoNodes(t, store)
	setNodeCPU(t, store, a, "T2CL", cascadeSig, 0)
	notif := &sched.FakeNotifier{}
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, notif, "1.10.0")
	decode := func() map[string]any {
		t.Helper()
		raw, ok := notif.LastPayload(db.NotifySnapshotWritten)
		if !ok {
			t.Fatal("no snapshot_written notification")
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	e.XEmitSnapshotWritten(ctx, a)
	if payload := decode(); payload["cpu_template"] != "T2CL" || payload["cpu_model"] != cascadeSig {
		t.Fatalf("snapshot_written cpu identity = %v / %v", payload["cpu_template"], payload["cpu_model"])
	}
	e.XEmitSnapshotWritten(ctx, "unregistered")
	if payload := decode(); payload["cpu_template"] != nil {
		t.Fatalf("an unknown node must leave a legacy row, got %v", payload)
	}
}

// Amendment 1: a park reuses a live row only when it restores on the node the
// instance runs on, so a new CPU platform captures its own snapshot.
func TestParkReuseIsPerNodePlatform(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	intelNode, amdNode := sched.SeedTwoNodes(t, store)
	setNodeCPU(t, store, intelNode, "T2CL", cascadeSig, 0)
	setNodeCPU(t, store, amdNode, "", milanSig, 0)
	_, _, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	createCPUSnapshot(t, store, dep.ID, "intel", "T2CL", iceSig, time.Time{})
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	if _, ok := e.XReusableSnapshotOn(ctx, dep.ID, state.SnapshotTierInit, intelNode); !ok {
		t.Fatal("a T2CL row must be reusable on a T2CL Cascade Lake node")
	}
	if _, ok := e.XReusableSnapshotOn(ctx, dep.ID, state.SnapshotTierInit, amdNode); ok {
		t.Fatal("the AMD node must capture its own snapshot, not reuse the Intel one")
	}
	createCPUSnapshot(t, store, dep.ID, "amd", "", milanSig, time.Time{})
	snap, ok := e.XReusableSnapshotOn(ctx, dep.ID, state.SnapshotTierInit, amdNode)
	if !ok || snap.CPUModel != milanSig {
		t.Fatalf("after its capture the AMD node reuses its own row, got %+v %v", snap, ok)
	}
	rows, err := store.LiveSnapshotsForTier(ctx, dep.ID, state.SnapshotTierInit)
	if err != nil || len(rows) != 2 {
		t.Fatalf("both platforms keep a live row, got %d rows, %v", len(rows), err)
	}
}

// Amendment 1: when every node that can restore the newest row is full, the
// wake restores another platform's row on a node that has room.
func TestWakeRestoresAnotherPlatformsSnapshotWhenTheFirstIsFull(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	intelNode, amdNode := sched.SeedTwoNodes(t, store)
	setNodeCPU(t, store, intelNode, "T2CL", iceSig, 1024)
	setNodeCPU(t, store, amdNode, "", milanSig, 0)
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	base := time.Now().Add(-time.Hour)
	createCPUSnapshot(t, store, dep.ID, "amd", "", milanSig, base)
	createCPUSnapshot(t, store, dep.ID, "intel", "T2CL", cascadeSig, base.Add(time.Minute))
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	(*e.XCapacityTable()).Replace(sched.CapacityReport{NodeID: intelNode, UsedMB: 1024})
	got, err := e.Wake(ctx, app.ID, "", "", "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != amdNode || *vmm.Restores() != 1 || *vmm.ColdBoots() != 0 {
		t.Fatalf("wake node=%s restores=%d cold boots=%d; want a restore of the AMD row on the AMD node",
			got.NodeID, *vmm.Restores(), *vmm.ColdBoots())
	}
}
