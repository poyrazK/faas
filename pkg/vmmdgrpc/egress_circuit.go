package vmmdgrpc

// UpdateEgressCircuit handler (ADR-201 §3).

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// egressCircuitUpdater is the optional capability a VMM may expose. Declared
// as an assertion rather than added to the VMM interface so the existing test
// fakes — and any deployment that has not wired the breaker — keep compiling
// and cleanly report Unavailable instead of panicking.
type egressCircuitUpdater interface {
	UpdateEgressCircuit(ctx context.Context, appID string, targets []netns.EgressCircuitTarget) error
}

// UpdateEgressCircuit makes the open-circuit set of every live instance of an
// app exactly the supplied list. Whole-set semantics: an empty list closes
// every circuit. See the RPC docstring in vmmd.proto for why this is not a
// delta.
func (s *Server) UpdateEgressCircuit(ctx context.Context, req *vmmdpb.UpdateEgressCircuitRequest) (*vmmdpb.UpdateEgressCircuitAck, error) {
	const op = "UpdateEgressCircuit"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	if req.GetAppId() == "" {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing app_id", "app_id is required").
			WithDocs(wire.DocsBaseURL + "/vmmd#update-egress-circuit")))
	}
	updater, ok := s.vmm.(egressCircuitUpdater)
	if !ok {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(int(codes.Unavailable),
			"egress_circuit_unavailable", "Egress circuit updates unavailable",
			"vmmd egress-circuit live update is not wired")))
	}
	targets := toEgressCircuitTargets(req.GetCircuits())
	if err := updater.UpdateEgressCircuit(ctx, req.GetAppId(), targets); err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.UpdateEgressCircuitAck{}, nil
}

// toEgressCircuitTargets converts the wire form, SKIPPING anything that would
// not render into a well-formed nftables element.
//
// Skipping rather than erroring is deliberate. One malformed element fails the
// whole nft batch, which would leave the tenant's firewall in whatever state
// the partial batch produced; dropping the bad entry still applies every good
// circuit. A rejected request would instead leave every circuit unapplied,
// which fails in the direction of "dependency hangs come back".
func toEgressCircuitTargets(in []*vmmdpb.EgressCircuitTarget) []netns.EgressCircuitTarget {
	if len(in) == 0 {
		return nil
	}
	out := make([]netns.EgressCircuitTarget, 0, len(in))
	for _, t := range in {
		if t == nil {
			continue
		}
		target, err := netns.ParseEgressCircuitTarget(t.GetAddr(), int(t.GetPort()))
		if err != nil {
			continue
		}
		out = append(out, target)
	}
	return out
}
