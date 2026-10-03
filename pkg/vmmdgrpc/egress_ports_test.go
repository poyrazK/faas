// adr: 361 — per-app extra egress ports on the live convergence RPC.
package vmmdgrpc_test

import (
	"context"
	"reflect"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type egressPortsVMM struct {
	*fakeVMM
	calls [][]uint16
}

func (f *egressPortsVMM) UpdateEgressPorts(_ context.Context, _ string, extra []uint16) error {
	f.calls = append(f.calls, append([]uint16(nil), extra...))
	return nil
}

func TestUpdateEgressAllowlist_EgressPorts(t *testing.T) {
	ctx := context.Background()
	vmm := &egressPortsVMM{fakeVMM: &fakeVMM{}}
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)

	// An older schedd never sends ports: the flag is false, nothing changes.
	if _, err := s.UpdateEgressAllowlist(ctx, &vmmdpb.UpdateEgressAllowlistRequest{AppId: "app-1"}); err != nil {
		t.Fatalf("allowlist-only update: %v", err)
	}
	if len(vmm.calls) != 0 {
		t.Fatalf("a request without egress_ports_set updated ports: %v", vmm.calls)
	}
	if _, err := s.UpdateEgressAllowlist(ctx, &vmmdpb.UpdateEgressAllowlistRequest{
		AppId: "app-1", EgressPorts: []uint32{5432, 70000, 0}, EgressPortsSet: true,
	}); err != nil {
		t.Fatalf("ports update: %v", err)
	}
	if !reflect.DeepEqual(vmm.calls, [][]uint16{{5432}}) {
		t.Fatalf("ports reaching vmmd = %v, want [[5432]] (out-of-range values dropped)", vmm.calls)
	}
	// An explicit empty list clears the extra ports.
	if _, err := s.UpdateEgressAllowlist(ctx, &vmmdpb.UpdateEgressAllowlistRequest{AppId: "app-1", EgressPortsSet: true}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(vmm.calls) != 2 || len(vmm.calls[1]) != 0 {
		t.Fatalf("clear did not reach vmmd as an empty update: %v", vmm.calls)
	}
}

// A vmmd without the live updater refuses rather than silently keeping stale
// ports while reporting success.
func TestUpdateEgressAllowlist_EgressPortsUnimplemented(t *testing.T) {
	s := vmmdgrpc.New(&fakeVMM{}, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	_, err := s.UpdateEgressAllowlist(context.Background(), &vmmdpb.UpdateEgressAllowlistRequest{
		AppId: "app-1", EgressPorts: []uint32{5432}, EgressPortsSet: true,
	})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("err = %v, want Unimplemented", err)
	}
}
