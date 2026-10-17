// adr: 570
package vmmdgrpc_test

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type circuitVMM struct {
	*fakeVMM
	calls    int
	err      error
	revision int64
}

func (f *circuitVMM) UpdateEgressCircuit(context.Context, string, []netns.EgressCircuitTarget) error {
	f.calls++
	return f.err
}
func (f *circuitVMM) UpdateEgressCircuitRevision(_ context.Context, _ string, snapshot netns.EgressCircuitSnapshot) (int64, error) {
	f.calls++
	f.revision = snapshot.Revision
	return snapshot.Revision, f.err
}

func TestEgressCircuitRejectsMalformedWholeSet(t *testing.T) {
	for _, target := range []*vmmdpb.EgressCircuitTarget{nil, {Addr: "bad", Port: 5432}, {Addr: "203.0.113.9", Port: 70000}, {Addr: "203.0.113.9", Port: 0}} {
		vmm := &circuitVMM{fakeVMM: &fakeVMM{}}
		s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("circuit_test"), "test", nil)
		_, err := s.UpdateEgressCircuit(t.Context(), &vmmdpb.UpdateEgressCircuitRequest{AppId: "app", Revision: 1, Circuits: []*vmmdpb.EgressCircuitTarget{{Addr: "203.0.113.9", Port: 5432}, target}})
		if status.Code(err) != codes.InvalidArgument || vmm.calls != 0 {
			t.Fatalf("malformed replacement reached VMM: calls=%d err=%v", vmm.calls, err)
		}
	}
}

func TestEgressCircuitAcknowledgesRevisionAndDisabledNode(t *testing.T) {
	vmm := &circuitVMM{fakeVMM: &fakeVMM{}}
	s := vmmdgrpc.New(vmm, wire.NewOpsMetrics("circuit_test"), "test", nil)
	req := &vmmdpb.UpdateEgressCircuitRequest{AppId: "app", Revision: 7}
	ack, err := s.UpdateEgressCircuit(t.Context(), req)
	if err != nil || ack.GetRevision() != 7 || vmm.revision != 7 {
		t.Fatalf("revision acknowledgment=%v err=%v", ack, err)
	}
	vmm.err = fcvm.ErrEgressCircuitDisabled
	if _, err := s.UpdateEgressCircuit(t.Context(), req); status.Code(err) != codes.Unavailable {
		t.Fatalf("disabled node reported enforcement: %v", err)
	}
	vmm.err = fcvm.ErrEgressCircuitRevision
	if _, err := s.UpdateEgressCircuit(t.Context(), req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bad revision status: %v", err)
	}
}
