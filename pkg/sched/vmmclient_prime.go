package sched

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PrimeInstance wraps vmmd's PrimeInstance RPC (ADR-20261114153000). A vmmd
// from before the RPC answers a bare Unimplemented status; that becomes a
// 501 CodeNotImplemented Problem so the engine reads it as "unsupported".
func (c *VMMClient) PrimeInstance(ctx context.Context, instance, path string, requests int, budget time.Duration) (PrimeReport, error) {
	resp, err := c.cli.PrimeInstance(ctx, &vmmdpb.PrimeInstanceRequest{
		Instance:     instance,
		Path:         path,
		Requests:     int32(requests),
		BudgetMillis: budget.Milliseconds(),
	})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.Unimplemented {
			return PrimeReport{}, api.NewProblem(501, api.CodeNotImplemented,
				"Prime unavailable", "vmmd does not support init snapshot priming")
		}
		return PrimeReport{}, liftErr(err)
	}
	return PrimeReport{
		Sent:     int(resp.GetSent()),
		OK:       int(resp.GetOk()),
		Duration: time.Duration(resp.GetDurationMicros()) * time.Microsecond,
	}, nil
}

// PrimeInstance implements InitPrimeVMM by forwarding to the node's client.
func (r *VMMRouter) PrimeInstance(ctx context.Context, nodeID, instance, path string, requests int, budget time.Duration) (PrimeReport, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return PrimeReport{}, err
	}
	p, ok := cli.(interface {
		PrimeInstance(context.Context, string, string, int, time.Duration) (PrimeReport, error)
	})
	if !ok {
		return PrimeReport{}, api.NewProblem(501, api.CodeNotImplemented,
			"Prime unavailable", "vmmd client does not support init snapshot priming")
	}
	return p.PrimeInstance(ctx, instance, path, requests, budget)
}

var (
	_ InitPrimeVMM = (*VMMRouter)(nil)
	_              = interface {
		PrimeInstance(context.Context, string, string, int, time.Duration) (PrimeReport, error)
	}((*VMMClient)(nil))
)
