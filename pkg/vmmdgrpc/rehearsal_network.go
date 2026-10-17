// adr: 1349
package vmmdgrpc

import (
	"context"
	"regexp"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var rehearsalRunPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s *Server) requireRehearsalNetwork() error {
	vmm, ok := s.vmm.(interface{ RehearsalNetworkPolicyReady() bool })
	if !ok || !vmm.RehearsalNetworkPolicyReady() {
		return status.Error(codes.FailedPrecondition, "runtime rehearsal network policy authority is unavailable")
	}
	return nil
}

// Separate methods prevent older nodes from ignoring containment metadata.
// The Manager independently reads durable policy on every wake, including
// legacy callers. A response acknowledges the installed namespace's policy.
func (s *Server) CreateRehearsalColdBoot(ctx context.Context, req *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireRehearsalNetwork(); err != nil {
		return nil, err
	}
	if err := runtimeadmission.RejectUnknown(req); err != nil || !rehearsalRunPattern.MatchString(req.GetApp().GetRehearsalRunId()) || req.GetBuild() != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid rehearsal cold boot")
	}
	return s.CreateColdBoot(ctx, req)
}

func (s *Server) CreateRehearsalFromSnapshot(ctx context.Context, req *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireRehearsalNetwork(); err != nil {
		return nil, err
	}
	if err := runtimeadmission.RejectUnknown(req); err != nil || !rehearsalRunPattern.MatchString(req.GetApp().GetRehearsalRunId()) {
		return nil, status.Error(codes.InvalidArgument, "invalid rehearsal restore")
	}
	return s.CreateFromSnapshot(ctx, req)
}
