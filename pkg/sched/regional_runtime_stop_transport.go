package sched

// adr: 631
import (
	"context"
	"encoding/json"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

// WithRegionalRuntimePeerResolver binds verified server certificates to the
// exact original compute node. Wire once at construction, before RPCs begin.
func (r *VMMRouter) WithRegionalRuntimePeerResolver(resolver wire.NodeIdentityResolver) *VMMRouter {
	r.regionalRuntimePeers = resolver
	return r
}

func (r *VMMRouter) RetireRegionalRuntime(ctx context.Context, instance string, p runtimequota.CapacityPermit, fence string) (runtimequota.CapacityStop, error) {
	if p.Validate() != nil || !runtimequota.MatchesID(p.InstanceID, instance) ||
		(runtimequota.CapacityStop{Permit: p, FenceID: fence, AdmissionFrozen: true}).Validate() != nil {
		return runtimequota.CapacityStop{}, runtimequota.ErrBinding
	}
	if r.regionalRuntimePeers == nil {
		return runtimequota.CapacityStop{}, runtimequota.ErrTrust
	}
	cli, err := r.resolveFor(ctx, p.NodeID)
	if err != nil {
		return runtimequota.CapacityStop{}, err
	}
	// Only the client that verifies the response's actual TLS peer can produce
	// host stop evidence. A generic Destroy or alternate lifecycle fake cannot.
	native, ok := cli.(*VMMClient)
	if !ok {
		return runtimequota.CapacityStop{}, api.NewProblem(501, api.CodeNotImplemented,
			"Regional retirement unavailable", "vmmd client cannot authenticate native stop evidence")
	}
	return native.retireRegionalRuntime(ctx, instance, p, fence, r.regionalRuntimePeers)
}

func (c *VMMClient) retireRegionalRuntime(ctx context.Context, instance string, p runtimequota.CapacityPermit, fence string, resolver wire.NodeIdentityResolver) (runtimequota.CapacityStop, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return runtimequota.CapacityStop{}, err
	}
	var remote peer.Peer
	resp, err := c.cli.RetireRegionalRuntime(ctx, &vmmdpb.RetireRegionalRuntimeRequest{Instance: instance, Permit: raw, FenceId: fence}, grpc.Peer(&remote))
	if err != nil {
		return runtimequota.CapacityStop{}, liftErr(err)
	}
	cn, err := wire.PeerCN(peer.NewContext(ctx, &remote))
	if err != nil {
		return runtimequota.CapacityStop{}, runtimequota.ErrTrust
	}
	node, err := resolver.NodeIDByCN(cn)
	if err != nil || node != p.NodeID {
		return runtimequota.CapacityStop{}, runtimequota.ErrTrust
	}
	stop, err := runtimequota.DecodeCapacityStop(resp.GetStop())
	if err != nil || resp.GetInstance() != instance || stop.Permit != p {
		return runtimequota.CapacityStop{}, runtimequota.ErrTrust
	}
	if err := ctx.Err(); err != nil {
		return runtimequota.CapacityStop{}, err
	}
	return stop, nil // Preserve the host's first fence; never rewrite its evidence.
}
