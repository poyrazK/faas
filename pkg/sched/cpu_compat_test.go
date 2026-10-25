// adr: 20261024225612
package sched

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	cascadeSig = "cascadelake/GenuineIntel-6-85-7/aaaaaaaaaaaa"
	iceSig     = "icelake/GenuineIntel-6-106-6/bbbbbbbbbbbb"
	rocketSig  = "unknown/GenuineIntel-6-167-1/cccccccccccc"
)

func TestSnapshotRestorableOnRules(t *testing.T) {
	node := func(template, model string) state.ComputeNode {
		return state.ComputeNode{CPUTemplate: template, CPUModel: model}
	}
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
		{"T2CL capture on a T2CL Cascade Lake node", snap("T2CL", iceSig), node("T2CL", cascadeSig), true, true},
		{"T2CL capture on a template-less Ice Lake node", snap("T2CL", cascadeSig), node("", iceSig), true, true},
		{"T2CL capture on Rocket Lake", snap("T2CL", cascadeSig), node("", rocketSig), true, false},
		{"T2CL capture on a node that reports nothing", snap("T2CL", cascadeSig), node("", ""), true, false},
		{"template-less capture on the identical host", snap("", iceSig), node("T2CL", iceSig), true, true},
		{"template-less Ice Lake capture on Cascade Lake", snap("", iceSig), node("", cascadeSig), true, false},
		{"legacy row on a template-less node", snap("", ""), node("", cascadeSig), true, true},
		{"legacy row on an unreported node", snap("", ""), node("", ""), true, true},
		{"legacy row on a templated node", snap("", ""), node("T2CL", cascadeSig), true, false},
		{"legacy row in a mixed template-less fleet", snap("", ""), node("", cascadeSig), false, false},
	}
	for _, tc := range cases {
		if got := snapshotRestorableOn(tc.snap, tc.node, tc.legacyOK); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if !legacySnapshotsRestorable([]state.ComputeNode{node("", cascadeSig), node("", cascadeSig), node("", ""), node("T2CL", iceSig)}) {
		t.Error("one template-less model plus unreported and templated nodes must keep legacy rows restorable")
	}
	if legacySnapshotsRestorable([]state.ComputeNode{node("", cascadeSig), node("", iceSig)}) {
		t.Error("two template-less models must retire legacy restores")
	}
}

func TestCPUIdentityWarning(t *testing.T) {
	n := func(template, model string) state.ComputeNode {
		return state.ComputeNode{CPUTemplate: template, CPUModel: model}
	}
	for _, tc := range []struct {
		name  string
		nodes []state.ComputeNode
		warn  bool
	}{
		{"one T2CL cell across Cascade and Ice Lake", []state.ComputeNode{n("T2CL", cascadeSig), n("T2CL", iceSig)}, false},
		{"identical template-less nodes", []state.ComputeNode{n("", cascadeSig), n("", cascadeSig)}, false},
		{"unreported nodes are not counted", []state.ComputeNode{n("", ""), n("", cascadeSig)}, false},
		{"mixed templates", []state.ComputeNode{n("T2CL", cascadeSig), n("", cascadeSig)}, true},
		{"template-less nodes on different CPUs", []state.ComputeNode{n("", cascadeSig), n("", iceSig)}, true},
	} {
		if _, warning := CPUIdentityWarning(tc.nodes); (warning != "") != tc.warn {
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
			a: nodeCPU{"", rocketSig, 0, false}, b: nodeCPU{"", rocketSig, 0, false},
			wantRestore: false,
		},
		{
			name:         "compatible node full cold boots on the other",
			snapTemplate: "T2CL", snapModel: cascadeSig,
			a: nodeCPU{"T2CL", iceSig, 1024, true}, b: nodeCPU{"", rocketSig, 0, false},
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
			a, b := seedTwoNodes(t, store)
			setNodeCPU(t, store, a, tc.a.template, tc.a.model, tc.a.ceilingMB)
			setNodeCPU(t, store, b, tc.b.template, tc.b.model, tc.b.ceilingMB)
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
			if _, err := store.CreateSnapshot(ctx, state.Snapshot{
				DeploymentID: dep.ID, FCVersion: "1.10.0",
				StorageKey:  state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "cpu"),
				CPUTemplate: tc.snapTemplate, CPUModel: tc.snapModel,
			}); err != nil {
				t.Fatal(err)
			}
			vmm := &fakeVMM{}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			if tc.a.full {
				e.capacityTable.Replace(CapacityReport{NodeID: a, UsedMB: int32(tc.a.ceilingMB)})
			}
			got, err := e.Wake(ctx, app.ID, "", "", "gateway")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantRestore && (vmm.restores != 1 || vmm.coldBoots != 0) {
				t.Fatalf("restores=%d cold boots=%d, want one restore", vmm.restores, vmm.coldBoots)
			}
			if !tc.wantRestore && (vmm.restores != 0 || vmm.coldBoots != 1) {
				t.Fatalf("restores=%d cold boots=%d, want one cold boot", vmm.restores, vmm.coldBoots)
			}
			if want := map[string]string{"a": a, "b": b}[tc.wantNode]; want != "" && got.NodeID != want {
				t.Fatalf("wake node = %s, want %s", got.NodeID, want)
			}
		})
	}
}

// lastSnapshotWritten decodes the most recent snapshot_written payload.
func lastSnapshotWritten(t *testing.T, n *fakeNotifier) map[string]any {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.events) - 1; i >= 0; i-- {
		if n.events[i].channel == db.NotifySnapshotWritten {
			var out map[string]any
			if err := json.Unmarshal([]byte(n.events[i].payload), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatal("no snapshot_written notification")
	return nil
}

func TestSnapshotWrittenCarriesCapturingNodeCPU(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	a, _ := seedTwoNodes(t, store)
	setNodeCPU(t, store, a, "T2CL", cascadeSig, 0)
	notif := &fakeNotifier{}
	e := newEngine(t, store, &fakeVMM{}, notif, "1.10.0")
	e.emitSnapshotWritten(ctx, "ins", time.Time{}, "dep", a, "", "key", SnapshotBytes{}, state.SnapshotTierInit, nil)
	payload := lastSnapshotWritten(t, notif)
	if payload["cpu_template"] != "T2CL" || payload["cpu_model"] != cascadeSig {
		t.Fatalf("snapshot_written cpu identity = %v / %v", payload["cpu_template"], payload["cpu_model"])
	}
	e.emitSnapshotWritten(ctx, "ins", time.Time{}, "dep", "unregistered", "", "key", SnapshotBytes{}, state.SnapshotTierInit, nil)
	payload = lastSnapshotWritten(t, notif)
	if _, ok := payload["cpu_template"]; ok {
		t.Fatalf("an unknown node must leave a legacy row, got %v", payload)
	}
}

const milanSig = "milan/AuthenticAMD-25-1-1/dddddddddddd"

// Amendment 1: a park reuses a live row only when it restores on the node the
// instance runs on, so a new CPU platform captures its own snapshot.
func TestParkReuseIsPerNodePlatform(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	intelNode, amdNode := seedTwoNodes(t, store)
	setNodeCPU(t, store, intelNode, "T2CL", cascadeSig, 0)
	setNodeCPU(t, store, amdNode, "", milanSig, 0)
	_, _, dep := seedApp(t, store, api.PlanPro, 256, 5)
	create := func(name, template, model string) {
		t.Helper()
		if _, err := store.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, FCVersion: "1.10.0", Tier: state.SnapshotTierInit,
			StorageKey:  state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, name),
			CPUTemplate: template, CPUModel: model,
		}); err != nil {
			t.Fatal(err)
		}
	}
	create("intel", "T2CL", iceSig)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if _, ok := e.reusableSnapshotOn(ctx, dep.ID, state.SnapshotTierInit, intelNode); !ok {
		t.Fatal("a T2CL row must be reusable on a T2CL Cascade Lake node")
	}
	if _, ok := e.reusableSnapshotOn(ctx, dep.ID, state.SnapshotTierInit, amdNode); ok {
		t.Fatal("the AMD node must capture its own snapshot, not reuse the Intel one")
	}
	create("amd", "", milanSig)
	snap, ok := e.reusableSnapshotOn(ctx, dep.ID, state.SnapshotTierInit, amdNode)
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
	intelNode, amdNode := seedTwoNodes(t, store)
	setNodeCPU(t, store, intelNode, "T2CL", iceSig, 1024)
	setNodeCPU(t, store, amdNode, "", milanSig, 0)
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	base := time.Now().Add(-time.Hour)
	for _, row := range []struct {
		name, template, model string
		at                    time.Time
	}{{"amd", "", milanSig, base}, {"intel", "T2CL", cascadeSig, base.Add(time.Minute)}} {
		if _, err := store.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, FCVersion: "1.10.0", Tier: state.SnapshotTierInit, CreatedAt: row.at,
			StorageKey:  state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, row.name),
			CPUTemplate: row.template, CPUModel: row.model,
		}); err != nil {
			t.Fatal(err)
		}
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	e.capacityTable.Replace(CapacityReport{NodeID: intelNode, UsedMB: 1024})
	got, err := e.Wake(ctx, app.ID, "", "", "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != amdNode || vmm.restores != 1 || vmm.coldBoots != 0 {
		t.Fatalf("wake node=%s restores=%d cold boots=%d; want a restore of the AMD row on the AMD node",
			got.NodeID, vmm.restores, vmm.coldBoots)
	}
}
