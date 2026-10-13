// adr: 590
package vmmdgrpc_test

import (
	"context"
	"errors"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/metadata"
	"sync/atomic"
	"testing"
)

func TestCheckpointWakeRPCRejectsBeforeVMEffects(t *testing.T) {
	calls := &atomic.Int32{}
	f := &fakeVMM{wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
		calls.Add(1)
		return nil, errors.New("unexpected VM effect")
	}}
	cli, _ := newServer(t, f)
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("x-faas-checkpoint-wake-version", "1"))
	app := &vmmdpb.AppSpec{BaseKey: "base", LayerKey: "layer", VcpuCount: 2, MemSizeMib: 256}
	if out, err := cli.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{Instance: "instance", App: app}); err == nil || out != nil {
		t.Fatal("tagged boot downgraded to ordinary RPC", err)
	}
	if out, err := cli.CreateFromSnapshot(ctx, &vmmdpb.CreateFromSnapshotRequest{Instance: "instance", App: app}); err == nil || out != nil {
		t.Fatal("tagged restore downgraded to ordinary RPC", err)
	}
	if calls.Load() != 0 {
		t.Fatal("unsupported ownership reached VM effects")
	}
}
