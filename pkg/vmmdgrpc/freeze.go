package vmmdgrpc

// Frozen tier RPCs (ADR-1151). schedd owns the FROZEN state and thaw
// admission; vmmd pauses and resumes the guest in place (fcvm.Manager.Freeze
// and Thaw), reports the state in InstanceStats, and fails a forward to a
// frozen guest fast instead of letting it hang.

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// freezer and thawer are the ADR-1151 capabilities fcvm.Manager provides.
type freezer interface {
	Freeze(ctx context.Context, instance string, opts fcvm.FreezeOptions) (fcvm.FreezeReport, error)
}

type thawer interface {
	ThawWith(ctx context.Context, instance string, opts fcvm.ThawOptions) error
}

// frozenGate lets a forward wait out a freeze in progress and learn whether
// the guest ended up frozen.
type frozenGate interface {
	AwaitFrozen(ctx context.Context, instance string) (bool, error)
}

// frozenReporter reports an instance's frozen state for InstanceStats.
type frozenReporter interface {
	FrozenState(instance string) (frozen bool, since time.Time)
}

// FreezeInstance pauses an idle instance in place for the frozen tier
// (ADR-1151). Pooled forward sessions to the paused guest are dropped, and
// its stream bridge closes until the thaw starts it again.
func (s *Server) FreezeInstance(ctx context.Context, req *vmmdpb.FreezeInstanceRequest) (*vmmdpb.FreezeInstanceResponse, error) {
	const op = "FreezeInstance"
	start := time.Now()
	if req.GetInstance() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing instance", "instance is required on FreezeInstance")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	f, ok := s.vmm.(freezer)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Freeze unavailable", "vmmd does not expose in-place freeze")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	rep, err := f.Freeze(ctx, req.GetInstance(), fcvm.FreezeOptions{Reclaim: req.GetReclaim()})
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.suspendForwarding(ctx, req.GetInstance())
	return &vmmdpb.FreezeInstanceResponse{
		Instance:            req.GetInstance(),
		PauseMicros:         rep.Pause.Microseconds(),
		ReclaimMicros:       rep.Reclaim.Microseconds(),
		ResidentBytesBefore: rep.BeforeBytes,
		ResidentBytesAfter:  rep.AfterBytes,
	}, nil
}

// ThawInstance resumes a frozen instance in place (ADR-1151) after schedd
// admitted the thaw. With image_healthcheck_required, the instance's stored
// boot contract must require the healthcheck, and the thaw runs it.
func (s *Server) ThawInstance(ctx context.Context, req *vmmdpb.ThawInstanceRequest) (*vmmdpb.ThawInstanceResponse, error) {
	const op = "ThawInstance"
	start := time.Now()
	if req.GetInstance() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing instance", "instance is required on ThawInstance")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	t, ok := s.vmm.(thawer)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Thaw unavailable", "vmmd does not expose in-place thaw")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if req.GetImageHealthcheckRequired() {
		if p := s.imageHealthcheckContractProblem(req.GetInstance()); p != nil {
			s.ops.Observe(op, time.Since(start), p)
			return nil, grpcerr.ToStatus(p)
		}
	}
	// The bridge process starts while the guest resumes, as a restore's does
	// (wakeWithBridgePrewarm): otherwise the first request after the thaw
	// spawns it on its own path. A failed thaw drops it again.
	s.resumeStreamBridges(ctx, req.GetInstance())
	err := t.ThawWith(ctx, req.GetInstance(), fcvm.ThawOptions{RunResumeHook: req.GetRunAfterRestore()})
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		s.forgetForwarding(ctx, req.GetInstance())
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.ThawInstanceResponse{
		Instance:                 req.GetInstance(),
		ImageHealthcheckVerified: req.GetImageHealthcheckRequired(),
		AfterRestoreRan:          req.GetRunAfterRestore(),
	}, nil
}

// resumeStreamBridges starts a thawing instance's bridges again when the
// persistent v2 bridge is in use; the rollback switches keep the old path.
func (s *Server) resumeStreamBridges(ctx context.Context, instance string) {
	if s.streamBridges == nil || currentStreamBridgeVersion() != "v2" || !persistentStreamBridgeEnabled() {
		return
	}
	s.streamBridges.resume(ctx, instance)
}

// prepareGuest runs before a forward dials its guest; the caller has marked
// the forward's activity. A frozen guest fails the forward fast with
// Unavailable and code instance_frozen, so the gateway wakes the instance
// through schedd; a forward never thaws it. A quiet-paused guest is resumed.
func (s *Server) prepareGuest(ctx context.Context, instanceID string) error {
	if err := s.refuseFrozen(ctx, instanceID); err != nil {
		return err
	}
	return s.resumeQuiet(ctx, instanceID)
}

// refuseFrozen fails a forward to a frozen instance. A freeze still in
// progress either sees the forward's activity and refuses, or pauses the
// guest; the check waits for that outcome.
func (s *Server) refuseFrozen(ctx context.Context, instanceID string) error {
	g, ok := s.vmm.(frozenGate)
	if !ok {
		return nil
	}
	frozen, err := g.AwaitFrozen(ctx, instanceID)
	if err != nil {
		return status.FromContextError(err).Err()
	}
	if !frozen {
		return nil
	}
	return grpcerr.ToStatus(api.NewProblem(503, api.CodeInstanceFrozen,
		"Instance frozen", "the instance is frozen; schedd thaws it on the next wake"))
}

// addFrozenState fills InstanceStats.frozen(_since) (ADR-1151).
func (s *Server) addFrozenState(row *vmmdpb.InstanceStats) {
	r, ok := s.vmm.(frozenReporter)
	if !ok {
		return
	}
	frozen, since := r.FrozenState(row.GetInstance())
	if !frozen {
		return
	}
	row.Frozen = true
	if !since.IsZero() {
		row.FrozenSince = timestamppb.New(since)
	}
}
