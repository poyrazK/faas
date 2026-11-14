package vmmdgrpc

// Init snapshot priming (ADR-20261114153000): schedd asks vmmd to GET a
// live instance's own HTTP path a few times before its first init capture.

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
)

// primer is the fcvm.Manager capability behind PrimeInstance.
type primer interface {
	Prime(ctx context.Context, instance string, opts fcvm.PrimeOptions) (fcvm.PrimeReport, error)
}

// PrimeInstance runs one priming burst against a live instance. It changes no
// lifecycle state; schedd captures the snapshot whatever this returns.
func (s *Server) PrimeInstance(ctx context.Context, req *vmmdpb.PrimeInstanceRequest) (*vmmdpb.PrimeInstanceResponse, error) {
	const op = "PrimeInstance"
	start := time.Now()
	if req.GetInstance() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing instance", "instance is required on PrimeInstance")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	p, ok := s.vmm.(primer)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Prime unavailable", "vmmd does not expose init snapshot priming")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	rep, err := p.Prime(ctx, req.GetInstance(), fcvm.PrimeOptions{
		Path:     req.GetPath(),
		Requests: int(req.GetRequests()),
		Budget:   time.Duration(req.GetBudgetMillis()) * time.Millisecond,
	})
	if errors.Is(err, fcvm.ErrPrimeInvalidPath) {
		problem := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid prime path", "path must start with / on PrimeInstance")
		s.ops.Observe(op, time.Since(start), problem)
		return nil, grpcerr.ToStatus(problem)
	}
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.PrimeInstanceResponse{
		Instance:       req.GetInstance(),
		Sent:           int32(rep.Sent),
		Ok:             int32(rep.OK),
		DurationMicros: rep.Duration.Microseconds(),
	}, nil
}
