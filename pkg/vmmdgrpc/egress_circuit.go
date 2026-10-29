package vmmdgrpc

// UpdateEgressCircuit handler (ADR-201 §3).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
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

type egressCircuitRevisionUpdater interface {
	UpdateEgressCircuitRevision(context.Context, string, netns.EgressCircuitSnapshot) (int64, error)
}

// UpdateEgressCircuit makes the open-circuit set of every live instance of an
// app exactly the supplied list. Whole-set semantics: an empty list closes
// every circuit. See the RPC docstring in vmmd.proto for why this is not a
// delta.
func (s *Server) UpdateEgressCircuit(ctx context.Context, req *vmmdpb.UpdateEgressCircuitRequest) (_ *vmmdpb.UpdateEgressCircuitAck, returnErr error) {
	const op = "UpdateEgressCircuit"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), returnErr) }()

	if req.GetAppId() == "" {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(http.StatusBadRequest,
			api.CodeValidation, "Missing app_id", "app_id is required").
			WithDocs(wire.DocsBaseURL + "/vmmd#update-egress-circuit")))
	}
	updater, ok := s.vmm.(egressCircuitUpdater)
	if !ok {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(http.StatusServiceUnavailable,
			api.CodeEgressCircuitUnavailable, "Egress circuit updates unavailable",
			"vmmd egress-circuit live update is not wired")))
	}
	targets, err := toEgressCircuitTargets(req.GetCircuits())
	if err != nil || req.GetRevision() < 0 {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid circuit policy", "every target and the policy revision must be valid")))
	}
	revision := req.GetRevision()
	if versioned, ok := s.vmm.(egressCircuitRevisionUpdater); ok {
		revision, err = versioned.UpdateEgressCircuitRevision(ctx, req.GetAppId(), netns.EgressCircuitSnapshot{Revision: revision, Targets: targets})
	} else if revision != 0 {
		err = fcvm.ErrEgressCircuitRevision
	} else {
		err = updater.UpdateEgressCircuit(ctx, req.GetAppId(), targets)
	}
	if err := egressCircuitUpdateError(err); err != nil {
		return nil, err
	}
	return &vmmdpb.UpdateEgressCircuitAck{Revision: revision}, nil
}

func egressCircuitUpdateError(err error) error {
	if errors.Is(err, fcvm.ErrEgressCircuitDisabled) {
		return grpcerr.ToStatus(toProblem(api.NewProblem(http.StatusServiceUnavailable, api.CodeEgressCircuitDisabled,
			"Egress circuit enforcement disabled", "enable FAAS_EGRESS_CIRCUIT_BREAKER on this compute node")))
	}
	if errors.Is(err, fcvm.ErrEgressCircuitRevision) {
		return grpcerr.ToStatus(toProblem(api.NewProblem(http.StatusUnprocessableEntity, api.CodeEgressCircuitRevision,
			"Invalid circuit revision", "a committed, consistent desired-policy revision is required")))
	}
	return grpcerr.ToStatus(toProblem(err))
}

func (s *Server) egressCircuitEnforcement(instance string) *vmmdpb.EgressCircuitEnforcement {
	provider, ok := s.vmm.(interface {
		EgressCircuitStatus(string) (fcvm.EgressCircuitStatus, bool)
	})
	if !ok {
		return nil
	}
	state, present := provider.EgressCircuitStatus(instance)
	if !present {
		return nil
	}
	return &vmmdpb.EgressCircuitEnforcement{
		Enabled: state.Enabled, Applied: state.Applied,
		DesiredRevision: state.DesiredRevision, AppliedRevision: state.AppliedRevision,
		TargetCount: int32(state.TargetCount),
	}
}

// Reject a malformed complete-set update before mutating any namespace.
func toEgressCircuitTargets(in []*vmmdpb.EgressCircuitTarget) ([]netns.EgressCircuitTarget, error) {
	if len(in) > api.EgressCircuitMaxTargets {
		return nil, fmt.Errorf("too many egress circuit targets")
	}
	out := make([]netns.EgressCircuitTarget, 0, len(in))
	for _, t := range in {
		if t == nil {
			return nil, fmt.Errorf("nil egress circuit target")
		}
		target, err := netns.ParseEgressCircuitTarget(t.GetAddr(), int(t.GetPort()))
		if err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return netns.CanonicalEgressCircuitTargets(out)
}
