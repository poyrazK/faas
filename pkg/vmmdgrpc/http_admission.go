package vmmdgrpc

import (
	"context"
	"errors"
	"net/http"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type httpForwardAdmissionVMM interface {
	AcquireHTTPForward(context.Context, string) (context.Context, func(), error)
}

func (s *Server) httpAdmissionEnforcement(instance string) *vmmdpb.HTTPAdmissionEnforcement {
	provider, ok := s.vmm.(interface {
		HTTPAdmissionStatus(string) (fcvm.HTTPAdmissionStatus, bool)
	})
	if !ok {
		return nil
	}
	state, present := provider.HTTPAdmissionStatus(instance)
	if !present {
		return nil
	}
	return &vmmdpb.HTTPAdmissionEnforcement{Enabled: state.Enabled, Limit: int64(state.Limit), Inflight: int64(state.Inflight),
		Generation: state.Generation, Retiring: state.Retiring, Plan: string(state.Plan)}
}

func admitHTTPForward[Req, Resp any](s *Server, stream grpc.BidiStreamingServer[Req, Resp], instance string) (grpc.BidiStreamingServer[Req, Resp], func(), error) {
	owner, ok := s.vmm.(httpForwardAdmissionVMM)
	if !ok {
		return nil, nil, httpAdmissionUnavailable()
	}
	ctx, release, err := owner.AcquireHTTPForward(stream.Context(), instance)
	if err != nil {
		switch {
		case errors.Is(err, fcvm.ErrHTTPForwardCapacity):
			p := api.NewProblem(http.StatusTooManyRequests, api.CodeConcurrencyThrottled,
				"App busy", "The instance has no free request capacity.")
			var capacity *fcvm.HTTPForwardCapacityError
			if errors.As(err, &capacity) {
				p = p.WithLimit(int64(capacity.Limit), int64(capacity.Observed))
			}
			p.DocsURL = "https://gregale.dev/docs/limits#concurrency"
			err = grpcerr.ToStatus(p)
		case errors.Is(err, fcvm.ErrHTTPForwardNotLive):
			err = status.Error(codes.NotFound, "instance is unavailable for HTTP forwarding")
		case errors.Is(err, fcvm.ErrHTTPForwardUntrusted):
			err = httpAdmissionUnavailable()
		default:
			err = status.FromContextError(err).Err()
		}
		return nil, nil, err
	}
	return &admittedHTTPStream[Req, Resp]{BidiStreamingServer: stream, ctx: ctx}, release, nil
}

func httpAdmissionUnavailable() error {
	return grpcerr.ToStatus(api.NewProblem(http.StatusServiceUnavailable, api.CodeHTTPAdmissionUnavailable,
		"Request admission unavailable", "The instance request limit cannot be verified."))
}

// A lifecycle drain cancels its permit context without closing the client's
// entire gRPC connection. Unblock the bridge's I/O on that cancellation;
// gRPC reclaims any pending transport call when the handler returns.
type admittedHTTPStream[Req, Resp any] struct {
	grpc.BidiStreamingServer[Req, Resp]
	ctx context.Context
}

func (s *admittedHTTPStream[Req, Resp]) Context() context.Context { return s.ctx }

func (s *admittedHTTPStream[Req, Resp]) Recv() (*Req, error) {
	type result struct {
		msg *Req
		err error
	}
	done := make(chan result, 1)
	go func() { msg, err := s.BidiStreamingServer.Recv(); done <- result{msg, err} }()
	select {
	case <-s.ctx.Done():
		return nil, status.FromContextError(s.ctx.Err()).Err()
	case r := <-done:
		return r.msg, r.err
	}
}

func (s *admittedHTTPStream[Req, Resp]) Send(msg *Resp) error {
	done := make(chan error, 1)
	go func() { done <- s.BidiStreamingServer.Send(msg) }()
	select {
	case <-s.ctx.Done():
		return status.FromContextError(s.ctx.Err()).Err()
	case err := <-done:
		return err
	}
}
