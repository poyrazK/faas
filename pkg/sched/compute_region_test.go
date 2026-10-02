// adr: 424
package sched

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestComputeRegionOverridesAffinityAndRejectsOverflow(t *testing.T) {
	fsn, hel := "eu-fsn", "eu-hel"
	nodes := []state.ComputeNode{node("fsn", "fsn", 0, 100), node("hel", "hel", 0, 1000)}
	nodes[0].Region, nodes[1].Region = &fsn, &hel
	request := Request{RAMMB: 40, RequiredRegion: fsn, PreferredRegion: hel, PreferredNodeID: "hel", PreferredNodeIDs: []string{"hel"}, PrioritizeSnapshotLocality: true}
	placement, err := ChoosePlacement(nodes, nil, nil, request)
	if err != nil || placement.NodeID != "fsn" {
		t.Fatalf("placement=%+v err=%v", placement, err)
	}
	if _, err := ChoosePlacement(nodes, map[string]int64{"fsn": 100}, nil, request); err == nil {
		t.Fatal("full selected region spilled into another region")
	}
	nodes[0].Region = nil
	if _, err := ChoosePlacement(nodes, nil, nil, request); err == nil {
		t.Fatal("unlabelled node admitted")
	}
}

func FuzzComputeRegionPlacement(f *testing.F) {
	f.Add([]byte{0, 1, 1, 100, 2, 1, 255})
	f.Add([]byte{1, 0, 1, 0, 0, 0, 255})
	f.Add([]byte{})
	// Run varied fleet shapes as part of the ordinary suite too, without
	// requiring the optional, instrumented fuzz build on every developer host.
	random := rand.New(rand.NewSource(424))
	for i := 0; i < 128; i++ {
		fleet := make([]byte, 64)
		_, _ = random.Read(fleet)
		f.Add(fleet)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		regions := []string{"eu-fsn", "eu-hel"}
		required := regions[0]
		if len(input) > 0 {
			required = regions[int(input[0])%2]
		}
		nodes := make([]state.ComputeNode, 0, len(input)/3)
		fits := false
		for i := 1; i+2 < len(input) && len(nodes) < 128; i += 3 {
			id := fmt.Sprintf("node-%d", i)
			n := node(id, id, 0, int(input[i+2]))
			n.Active = input[i+1]%2 == 1
			if input[i]%3 < 2 {
				n.Region = &regions[int(input[i])%3]
			}
			if n.Active && state.ComputeRegionAllowsNode(required, n) && n.AdmissionCeilingMB >= 40+api.PerVMOverheadMB {
				fits = true
			}
			nodes = append(nodes, n)
		}
		r := Request{RAMMB: 40, RequiredRegion: required, PreferredRegion: regions[1], PreferredNodeID: "node-1", PreferredNodeIDs: []string{"node-4"}, PrioritizeSnapshotLocality: true}
		placement, err := ChoosePlacement(nodes, nil, nil, r)
		if (err == nil) != fits {
			t.Fatalf("fits=%v placement=%+v err=%v", fits, placement, err)
		}
		if err == nil && placement.Region != required {
			t.Fatalf("placement escaped %s: %+v", required, placement)
		}
	})
}

func TestComputeRegionMigrationRefusesBeforePausingSource(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	instanceID := seedInstanceForMigration(t, store, "dying")
	otherRegion := "eu-hel"
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "other-region", Region: &otherRegion, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarnessForTest(t, store, vmm, node.ID)
	h.specBuilder = func(ctx context.Context, instanceID string) (AppSpec, error) {
		spec, err := stubSpecBuilder(ctx, instanceID)
		spec.ComputeRegion = "eu-fsn"
		return spec, err
	}
	if err := h.MigrateOne(t.Context(), instanceID, "dying"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("migration = %v, want ErrConflict", err)
	}
	if vmm.prepares != 0 || vmm.adopts != 0 || vmm.cancels != 0 {
		t.Fatalf("cross-region migration reached VMM: prepares=%d adopts=%d cancels=%d", vmm.prepares, vmm.adopts, vmm.cancels)
	}
}

func TestComputeRegionClaimAndRecovery(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "regional@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	fsn, hel := "eu-fsn", "eu-hel"
	local, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "fsn", TargetURL: "unix:///fsn", VPCPUs: 4, VCPUBudget: 160, AdmissionCeilingMB: 1024, Region: &fsn, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "hel", TargetURL: "unix:///hel", VPCPUs: 40, VCPUBudget: 160, AdmissionCeilingMB: 10000, Region: &hel, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: fsn, DisplayName: "Falkenstein", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "regional-app", RAMMB: 128, MaxConcurrency: 1, ComputeRegion: fsn})
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, store, &fakeVMM{}, nil, "1.10.0")
	if err := engine.ClaimUnplaced(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	app, err = store.AppByID(ctx, app.ID)
	if err != nil || app.NodeID != local.ID {
		t.Fatalf("owner=%s err=%v", app.NodeID, err)
	}
	if err := store.SetComputeNodeActive(ctx, local.ID, false); err != nil {
		t.Fatal(err)
	}
	recovery := newEngine(t, store, &fakeVMM{}, nil, "1.10.0").WithOwnerNodeID(remote.ID)
	if err := recovery.RebalanceOrphanedApps(ctx, local.ID); err != nil {
		t.Fatal(err)
	}
	app, err = store.AppByID(ctx, app.ID)
	if err != nil || app.NodeID != local.ID {
		t.Fatalf("out-of-region recovery changed owner: %s %v", app.NodeID, err)
	}
}

func TestComputeRegionPlacementRefusalCodes(t *testing.T) {
	fsn, hel := "eu-fsn", "eu-hel"
	home, remote := node("home", "home", 0, 100), node("remote", "remote", 0, 1000)
	home.Region, remote.Region = &fsn, &hel
	for _, tc := range []struct {
		name  string
		nodes []state.ComputeNode
		used  map[string]int64
		code  string
	}{
		{"outage", []state.ComputeNode{remote}, nil, api.CodeComputeRegionUnavailable},
		{"exhausted", []state.ComputeNode{home, remote}, map[string]int64{"home": 100}, api.CodeComputeRegionCapacityExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ChoosePlacement(tc.nodes, tc.used, nil, Request{RAMMB: 40, RequiredRegion: fsn})
			p := api.AsProblem(err)
			if p == nil || p.Code != tc.code || p.ComputeRegion != fsn {
				t.Fatalf("problem=%+v err=%v", p, err)
			}
		})
	}
}

func TestComputeRegionRecoverySlotsAreCapacityRefusal(t *testing.T) {
	// The engine counted healthy regional nodes before filtering peers whose
	// recovery slots were exhausted. Empty filtered candidates are not an outage.
	snapshot := placementSnapshot{requiredRegion: "eu-fsn", eligibleRegionalNodes: 1}
	_, err := snapshot.choose(Request{RAMMB: 128})
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeComputeRegionCapacityExhausted {
		t.Fatalf("recovery-slot exhaustion misreported as outage: %v", err)
	}
}
