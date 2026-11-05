package vmmdgrpc

// AdoptionReport serves the inventory a restarted vmmd took over (vmmd
// restart adoption ADR §3). schedd, the only writer to instances, fetches it
// once the node's ownership epoch moves and fails every live row the
// predecessor held that this vmmd did not adopt.

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// AdoptionReport is what one vmmd start adopted under its ownership epoch.
type AdoptionReport struct {
	NodeID                 string
	Epoch                  uint64
	Incarnation            string
	PredecessorIncarnation string
	// Fenced means the epoch was advanced outside this node's claims, so
	// nothing was adopted.
	Fenced   bool
	Verdicts []fcvm.AdoptionVerdict
}

// adoptionLeaser is the Manager's record of every instance leased since
// startup, the fence between survivors and guests this vmmd created.
type adoptionLeaser interface {
	RestartLeases() ([]string, bool)
}

// WithAdoptionReport turns the RPC on. A nil report means adoption is on but
// this start claimed no epoch, which the RPC answers UNAVAILABLE.
func (s *Server) WithAdoptionReport(report *AdoptionReport) *Server {
	if s != nil {
		s.adoptionReportOn, s.adoptionReport = true, report
	}
	return s
}

func (s *Server) AdoptionReport(ctx context.Context, _ *vmmdpb.AdoptionReportRequest) (*vmmdpb.AdoptionReportResponse, error) {
	if !s.adoptionReportOn {
		return nil, status.Error(codes.Unimplemented, "vmmd: restart adoption is off")
	}
	if err := authorizeSchedulerPeer(ctx, "the adoption report"); err != nil {
		return nil, err
	}
	r := s.adoptionReport
	if r == nil {
		return nil, status.Error(codes.Unavailable, "vmmd: this start claimed no ownership epoch")
	}
	resp := &vmmdpb.AdoptionReportResponse{
		NodeId: r.NodeID, Epoch: r.Epoch, Incarnation: r.Incarnation,
		PredecessorIncarnation: r.PredecessorIncarnation, Fenced: r.Fenced,
	}
	for _, v := range r.Verdicts {
		item := &vmmdpb.AdoptionReportInstance{Instance: v.Instance, AppId: v.AppID, Resolved: v.Resolved}
		if a := v.Admission; v.Adoptable && a != nil {
			item.Admission = &vmmdpb.AdoptionReportAdmission{
				Token: a.Token, IssuingIncarnation: a.IssuingIncarnation, CompletedAtUnixNano: a.CompletedAtUnixNano,
				Netns: a.Netns, HostIp: a.HostIP, LeaseUid: a.LeaseUID, Paused: a.Paused,
			}
		}
		if v.Adoptable {
			resp.Adopted = append(resp.Adopted, item)
			continue
		}
		// Not held: refused, or torn down (resolved "stopped").
		item.Reason, item.Detail = v.Reason, v.Detail
		resp.Refused = append(resp.Refused, item)
	}
	if leaser, ok := s.vmm.(adoptionLeaser); ok {
		resp.LeasedSinceStart, resp.Complete = leaser.RestartLeases()
	}
	return resp, nil
}

// adoptionInventorier is the Manager's pre-restart inventory.
type adoptionInventorier interface {
	AdoptionInventory(context.Context) ([]fcvm.AdoptionInventoryRecord, error)
}

// AdoptionInventory is refused on the daemon's ordinary listener: the
// records carry every guest's identity and are for the local candidate
// check only (AdoptionAdmin).
func (s *Server) AdoptionInventory(context.Context, *vmmdpb.AdoptionInventoryRequest) (*vmmdpb.AdoptionInventoryResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "vmmd: the adoption inventory is served on the local admin socket only")
}

// AdoptionAdmin is the service on vmmd's root-only admin socket: the
// adoption report and the pre-restart inventory, nothing else.
type AdoptionAdmin struct {
	vmmdpb.UnimplementedVmmdServer
	s *Server
}

// NewAdoptionAdmin serves s's adoption calls on the admin socket.
func NewAdoptionAdmin(s *Server) *AdoptionAdmin { return &AdoptionAdmin{s: s} }

func (a *AdoptionAdmin) AdoptionReport(ctx context.Context, req *vmmdpb.AdoptionReportRequest) (*vmmdpb.AdoptionReportResponse, error) {
	return a.s.AdoptionReport(ctx, req)
}

func (a *AdoptionAdmin) AdoptionInventory(ctx context.Context, _ *vmmdpb.AdoptionInventoryRequest) (*vmmdpb.AdoptionInventoryResponse, error) {
	inv, ok := a.s.vmm.(adoptionInventorier)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "vmmd: this VMM keeps no adoption inventory")
	}
	items, err := inv.AdoptionInventory(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	resp := &vmmdpb.AdoptionInventoryResponse{Records: make([]*vmmdpb.AdoptionInventoryRecord, 0, len(items))}
	for _, it := range items {
		resp.Records = append(resp.Records, &vmmdpb.AdoptionInventoryRecord{Record: it.Record, ProofOk: it.ProofOK, ProofReason: it.ProofReason, ProofDetail: it.ProofDetail})
	}
	return resp, nil
}
