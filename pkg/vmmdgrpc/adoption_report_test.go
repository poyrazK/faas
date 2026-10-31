package vmmdgrpc_test

import (
	"context"
	"net"
	"slices"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type leasingVMM struct {
	*fakeVMM
	leases   []string
	complete bool
}

func (v *leasingVMM) RestartLeases() ([]string, bool) { return v.leases, v.complete }

func TestAdoptionReportServesTheStartupInventory(t *testing.T) {
	local := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.UnixAddr{Net: "unix", Name: "vmmd.sock"}})
	vmm := &leasingVMM{fakeVMM: &fakeVMM{}, leases: []string{"new-1"}, complete: true}
	newServer := func() *vmmdgrpc.Server {
		return vmmdgrpc.New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil)
	}

	if _, err := newServer().AdoptionReport(local, &vmmdpb.AdoptionReportRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("adoption off: %v, want UNIMPLEMENTED", err)
	}
	if _, err := newServer().WithAdoptionReport(nil).AdoptionReport(local, &vmmdpb.AdoptionReportRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("no epoch claimed: %v, want UNAVAILABLE", err)
	}

	report := &vmmdgrpc.AdoptionReport{NodeID: "node", Epoch: 3, Incarnation: "inc-3", PredecessorIncarnation: "inc-2",
		Verdicts: []fcvm.AdoptionVerdict{
			{Instance: "kept", AppID: "app-1", Adoptable: true, Resolved: fcvm.AdoptionResolvedThawed},
			{Instance: "lost", AppID: "app-2", Reason: "process_gone", Detail: "pid exited"},
			{Instance: "stopped", AppID: "app-3", Resolved: fcvm.AdoptionResolvedStopped},
		}}
	s := newServer().WithAdoptionReport(report)
	if _, err := s.AdoptionReport(context.Background(), &vmmdpb.AdoptionReportRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no peer: %v, want UNAUTHENTICATED", err)
	}
	got, err := s.AdoptionReport(local, &vmmdpb.AdoptionReportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeId != "node" || got.Epoch != 3 || got.Incarnation != "inc-3" || got.PredecessorIncarnation != "inc-2" || got.Fenced {
		t.Fatalf("identity = %+v", got)
	}
	if len(got.Adopted) != 1 || got.Adopted[0].Instance != "kept" || got.Adopted[0].AppId != "app-1" || got.Adopted[0].Reason != "" || got.Adopted[0].Resolved != "thawed" {
		t.Fatalf("adopted = %+v", got.Adopted)
	}
	if len(got.Refused) != 2 || got.Refused[0].Instance != "lost" || got.Refused[0].Reason != "process_gone" || got.Refused[0].Detail != "pid exited" {
		t.Fatalf("refused = %+v", got.Refused)
	}
	if r := got.Refused[1]; r.Instance != "stopped" || r.Reason != "" || r.Resolved != "stopped" {
		t.Fatalf("finished stop = %+v, want listed as not held with resolved stopped", r)
	}
	if !got.Complete || !slices.Equal(got.LeasedSinceStart, []string{"new-1"}) {
		t.Fatalf("leases = %v complete=%v", got.LeasedSinceStart, got.Complete)
	}

	// An overflowed lease set is reported incomplete.
	vmm.leases, vmm.complete = nil, false
	if got, err := s.AdoptionReport(local, &vmmdpb.AdoptionReportRequest{}); err != nil || got.Complete {
		t.Fatalf("overflowed = %+v, %v", got, err)
	}
}
