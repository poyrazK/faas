package sched

// adr: 628
import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"google.golang.org/grpc"
)

type regionalMigrationRPC struct {
	vmmdpb.VmmdClient
	prepare *vmmdpb.PrepareLiveMigrationResponse
	cancel  *vmmdpb.CancelLiveMigrationResponse
}

func (c *regionalMigrationRPC) PrepareLiveMigration(_ context.Context, _ *vmmdpb.PrepareLiveMigrationRequest, _ ...grpc.CallOption) (*vmmdpb.PrepareLiveMigrationResponse, error) {
	return c.prepare, nil
}
func (c *regionalMigrationRPC) CancelLiveMigration(_ context.Context, _ *vmmdpb.CancelLiveMigrationRequest, _ ...grpc.CallOption) (*vmmdpb.CancelLiveMigrationResponse, error) {
	return c.cancel, nil
}

func TestRegionalMigrationRequiresExactTransportAcknowledgements(t *testing.T) {
	store, account, app, dep := regionalQuotaSchedulerFixture(t)
	freezeRegionalSchedulerApp(t, store, account, app)
	head, err := store.RegionalRuntimeQuotaHead(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := runtimequota.CapacityPermit{Grant: head.Grant, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.MustParse(dep.ID).String(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityServing}
	placement := runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
	raw, err := json.Marshal(placement)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"exact", "old-server", "wrong-instance", "wrong-lease", "wrong-seat"} {
		t.Run(scenario, func(t *testing.T) {
			rpc := &regionalMigrationRPC{prepare: &vmmdpb.PrepareLiveMigrationResponse{MemStorageKey: "snap/source/mem", VmstateStorageKey: "snap/source/vmstate", LeaseToken: "source-lease", RegionalRuntime: raw}, cancel: &vmmdpb.CancelLiveMigrationResponse{InstanceId: p.InstanceID, LeaseToken: "source-lease", RegionalRuntime: raw}}
			switch scenario {
			case "old-server":
				rpc.prepare.RegionalRuntime = nil
				rpc.cancel.RegionalRuntime = nil
			case "wrong-instance":
				rpc.cancel.InstanceId = uuid.NewString()
			case "wrong-lease":
				rpc.cancel.LeaseToken = "another-lease"
			case "wrong-seat":
				changed := placement.Clone()
				changed.Seat.ID = uuid.NewString()
				changedRaw, _ := json.Marshal(changed)
				rpc.prepare.RegionalRuntime = changedRaw
				rpc.cancel.RegionalRuntime = changedRaw
			}
			client := &VMMClient{cli: rpc}
			_, err := client.PrepareRegionalLiveMigration(t.Context(), p.InstanceID, "snap/source/mem", placement)
			if (scenario == "old-server" || scenario == "wrong-seat") != errors.Is(err, runtimequota.ErrTrust) {
				t.Fatal("capture acknowledgement", err)
			}
			err = client.CancelRegionalLiveMigration(t.Context(), p.InstanceID, "source-lease", placement)
			if (scenario != "exact") != errors.Is(err, runtimequota.ErrTrust) {
				t.Fatal("rollback acknowledgement", err)
			}
		})
	}
	var router *VMMRouter
	if err := router.CancelRegionalLiveMigration(t.Context(), uuid.NewString(), p.InstanceID, "lease", placement); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("cross-node rollback reached resolution", err)
	}
}
