package vmmdgrpc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type unavailableBuilderStore struct{ *state.MemStore }

func (unavailableBuilderStore) BuildByID(context.Context, string) (state.Build, error) {
	return state.Build{}, errors.New("database unavailable")
}

func TestBuilderDispatchPlacementColdAndWarm(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, scenario := range []string{"owner", "peer", "missing-node", "inactive", "cancelled", "unknown-build", "database-outage"} {
			name := "cold/" + scenario
			if warm {
				name = "warm/" + scenario
			}
			t.Run(name, func(t *testing.T) {
				store := state.NewMemStore()
				fx := conformance.Seed(t, store)
				region := "eu-builder"
				if _, err := store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: region, DisplayName: "Builders", AcceptingNewApps: true}); err != nil {
					t.Fatal(err)
				}
				node := fx.Node
				node.Region = &region
				node, err := store.UpsertComputeNodeFromOperator(fx.Ctx, node)
				if err != nil {
					t.Fatal(err)
				}
				app := fx.App
				app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "builder-dispatch", "", region
				app, err = store.CreateApp(fx.Ctx, app)
				if err != nil {
					t.Fatal(err)
				}
				dep, err := store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
				if err != nil {
					t.Fatal(err)
				}
				build, err := store.CreateBuild(fx.Ctx, dep.ID, dep.Kind, 10, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, build.ID, node.Name, 0); err != nil {
					t.Fatal(err)
				}
				nodeID := node.ID
				var view vmmdgrpc.BuilderClaimStore = store
				want := codes.FailedPrecondition
				switch scenario {
				case "owner":
					want = codes.OK
				case "peer":
					peer := node
					peer.ID, peer.Name = "", "peer-"+uuid.NewString()
					peer, err = store.CreateComputeNode(fx.Ctx, peer)
					if err != nil {
						t.Fatal(err)
					}
					nodeID = peer.ID
				case "missing-node":
					nodeID = ""
				case "inactive":
					if err := store.NodeSetLifecycle(fx.Ctx, node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
						t.Fatal(err)
					}
				case "cancelled":
					if err := store.MarkBuildCancelled(fx.Ctx, build.ID, "test", false, time.Now()); err != nil {
						t.Fatal(err)
					}
				case "unknown-build":
					build.ID = uuid.NewString()
				case "database-outage":
					view, want = unavailableBuilderStore{store}, codes.Unavailable
				}
				fake := &fakeVMM{}
				server := vmmdgrpc.New(fake, nil, "1.0.0", nil).WithNodeID(nodeID).WithBuilderClaimStore(view)
				instance := "build-" + build.ID
				appSpec := &vmmdpb.AppSpec{BaseKey: "builder-base", LayerKey: "builder-drive", VcpuCount: 2, MemSizeMib: 2048}
				buildSpec := &vmmdpb.BuildSpec{ExportDir: "/build/export", TimeoutSec: 600}
				if warm {
					_, err = server.CreateFromSnapshot(fx.Ctx, &vmmdpb.CreateFromSnapshotRequest{Instance: instance, App: appSpec, Build: buildSpec, Snapshot: &vmmdpb.SnapshotRef{StorageKey: "warm.mem", VmstateStorageKey: "warm.vmstate", FcVersion: "1.0.0"}})
				} else {
					_, err = server.CreateColdBoot(fx.Ctx, &vmmdpb.CreateColdBootRequest{Instance: instance, App: appSpec, Build: buildSpec})
				}
				if status.Code(err) != want {
					t.Fatalf("dispatch: %v, want %s", err, want)
				}
				wantLive := 0
				if want == codes.OK {
					wantLive = 1
				}
				if fake.live != wantLive {
					t.Fatalf("VMM calls produced %d live VMs, want %d", fake.live, wantLive)
				}
			})
		}
	}
}

func TestBuilderDispatchPreservesNonDurableIdentityPaths(t *testing.T) {
	for _, instance := range []string{"build-platform-probe", "devbuild-" + uuid.NewString()} {
		t.Run(instance, func(t *testing.T) {
			fake := &fakeVMM{}
			server := vmmdgrpc.New(fake, nil, "1.0.0", nil).WithBuilderClaimStore(state.NewMemStore())
			_, err := server.CreateColdBoot(t.Context(), &vmmdpb.CreateColdBootRequest{
				Instance: instance,
				App:      &vmmdpb.AppSpec{BaseKey: "builder-base", LayerKey: "builder-drive", VcpuCount: 2, MemSizeMib: 2048},
				Build:    &vmmdpb.BuildSpec{ExportDir: "/build/export", TimeoutSec: 600},
			})
			if err != nil || fake.live != 1 {
				t.Fatalf("non-durable builder: live=%d %v", fake.live, err)
			}
		})
	}
}
