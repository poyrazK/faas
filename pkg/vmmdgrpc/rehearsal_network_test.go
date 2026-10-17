// adr: 20261017201005
package vmmdgrpc_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type rehearsalCapableVMM struct{ *fakeVMM }

func (*rehearsalCapableVMM) RehearsalNetworkPolicyReady() bool { return true }

func TestRehearsalNetworkRPCPreservesPolicyAndInstalledAcknowledgement(t *testing.T) {
	const run = "abababababababababababababababab"
	var received fcvm.WakeRequest
	vmm := &rehearsalCapableVMM{fakeVMM: &fakeVMM{wakeFn: func(_ context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
		received = req
		return &fcvm.Instance{Net: netns.Config{Netns: "rehearsal", RehearsalRunID: req.RehearsalRunID}}, nil
	}}}
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("rehearsal_wire_test"), "1.10.0", nil)
	app := &vmmdpb.AppSpec{AppId: "app", RehearsalRunId: run, MemSizeMib: 128, VcpuCount: 2}
	ctx := admittedRPCContext(t)
	cold, err := s.CreateRehearsalColdBoot(ctx, &vmmdpb.CreateColdBootRequest{App: app, Instance: "cold", Plan: "hobby"})
	if err != nil || received.RehearsalRunID != run || cold.GetRehearsalRunId() != run {
		t.Fatalf("cold policy/acknowledgement: %+v %v", cold, err)
	}
	restored, err := s.CreateRehearsalFromSnapshot(ctx, &vmmdpb.CreateFromSnapshotRequest{App: app, Instance: "restore", Plan: "hobby", Snapshot: &vmmdpb.SnapshotRef{FcVersion: "1.10.0", StorageKey: "mem", VmstateStorageKey: "state"}})
	if err != nil || received.RehearsalRunID != run || restored.GetRehearsalRunId() != run || received.Snapshot == nil {
		t.Fatalf("restore policy/acknowledgement: %+v %v", restored, err)
	}
}

func TestRehearsalNetworkRPCRefusesLegacyBackendBeforeWake(t *testing.T) {
	wakes := 0
	vmm := &fakeVMM{wakeFn: func(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) { wakes++; return nil, nil }}
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("rehearsal_test"), "1.10.0", nil)
	ctx := admittedRPCContext(t)
	app := &vmmdpb.AppSpec{RehearsalRunId: "abababababababababababababababab"}
	_, err := s.CreateRehearsalColdBoot(ctx, &vmmdpb.CreateColdBootRequest{App: app, Instance: "test"})
	if status.Code(err) != codes.FailedPrecondition || wakes != 0 {
		t.Fatalf("legacy cold backend called: %d %v", wakes, err)
	}
	_, err = s.CreateRehearsalFromSnapshot(ctx, &vmmdpb.CreateFromSnapshotRequest{App: app, Instance: "test"})
	if status.Code(err) != codes.FailedPrecondition || wakes != 0 {
		t.Fatalf("legacy restore backend called: %d %v", wakes, err)
	}
}
