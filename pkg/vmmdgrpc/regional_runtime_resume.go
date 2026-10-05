package vmmdgrpc

// adr: 624
import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"google.golang.org/grpc/codes"
)

func (s *Server) resumeWarmRequest(ctx context.Context, instance string, p *runtimequota.Placement) error {
	if p != nil {
		if p.Seat == nil || !runtimequota.MatchesID(p.Permit.InstanceID, instance) {
			return api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
				"Invalid regional resume", "the issued serving placement must match the instance")
		}
		resumer, ok := s.vmm.(interface {
			ResumeRegionalRuntime(context.Context, string, runtimequota.Placement) error
		})
		if !ok {
			return api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
				"Regional resume unavailable", "vmmd does not expose explicit regional resume")
		}
		return resumer.ResumeRegionalRuntime(ctx, instance, p.Clone())
	}
	resumer, ok := s.vmm.(interface {
		ResumeVM(context.Context, string) error
	})
	if !ok {
		return api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Warm-pool resume unavailable", "vmmd does not expose in-place resume")
	}
	return resumer.ResumeVM(ctx, instance)
}
