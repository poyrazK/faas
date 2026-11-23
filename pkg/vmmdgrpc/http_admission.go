package vmmdgrpc

import (
	"context"
	"errors"
	"net/http"
	"sync"

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

// admitHTTPForward acquires the instance's forward permit and wraps stream so
// a lifecycle drain unblocks its I/O. As with any gRPC stream, at most one
// goroutine may call the returned stream's Recv and at most one its Send at a
// time; the wrapper's pumps rely on it (see admittedHTTPStream).
func admitHTTPForward[Req, Resp any](s *Server, stream grpc.BidiStreamingServer[Req, Resp], instance string) (grpc.BidiStreamingServer[Req, Resp], func(), error) {
	owner, ok := s.vmm.(httpForwardAdmissionVMM)
	if !ok {
		return nil, nil, httpAdmissionUnavailable()
	}
	ctx, release, err := owner.AcquireHTTPForward(stream.Context(), instance)
	if err != nil {
		switch {
		case errors.Is(err, fcvm.ErrHTTPForwardCapacity):
			limit, observed := 0, 0
			var capacity *fcvm.HTTPForwardCapacityError
			if errors.As(err, &capacity) {
				limit, observed = capacity.Limit, capacity.Observed
			}
			err = instanceBusy(limit, observed)
		case errors.Is(err, fcvm.ErrHTTPForwardNotLive):
			err = status.Error(codes.NotFound, "instance is unavailable for HTTP forwarding")
		case errors.Is(err, fcvm.ErrHTTPForwardUntrusted):
			err = httpAdmissionUnavailable()
		default:
			err = status.FromContextError(err).Err()
		}
		return nil, nil, err
	}
	return newAdmittedHTTPStream(stream, ctx), release, nil
}

// instanceBusy refuses a request the instance has no capacity for before it
// reaches the guest. A zero limit leaves the limit out of the problem.
func instanceBusy(limit, observed int) error {
	p := api.NewProblem(http.StatusTooManyRequests, api.CodeConcurrencyThrottled,
		"App busy", "The instance has no free request capacity.")
	if limit > 0 {
		p = p.WithLimit(int64(limit), int64(observed))
	}
	p.DocsURL = "https://gregale.dev/docs/plans#what-each-limit-means"
	return grpcerr.ToStatus(p)
}

func httpAdmissionUnavailable() error {
	return grpcerr.ToStatus(api.NewProblem(http.StatusServiceUnavailable, api.CodeHTTPAdmissionUnavailable,
		"Request admission unavailable", "The instance request limit cannot be verified."))
}

// A lifecycle drain cancels its permit context without closing the client's
// entire gRPC connection. Unblock the bridge's I/O on that cancellation;
// gRPC reclaims any pending transport call when the handler returns.
//
// The blocking gRPC calls run on two pump goroutines, one for Recv and one
// for Send, each started on first use and living for the stream. A goroutine
// per call (the earlier shape) started 4–5 goroutines per request, and each
// regrew its stack through gRPC and protobuf: 2.5% of vmmd's CPU on prod.
// The permit context is derived from the stream context, which gRPC cancels
// when the handler returns, so both pumps always exit.
type admittedHTTPStream[Req, Resp any] struct {
	grpc.BidiStreamingServer[Req, Resp]
	ctx context.Context

	recvOnce  sync.Once
	recvAsk   chan struct{}
	recvDone  chan admittedRecv[Req]
	recvStuck bool // a Recv was abandoned; the pump may still own the call

	sendOnce  sync.Once
	sendAsk   chan *Resp
	sendDone  chan error
	sendStuck bool // a Send was abandoned; the pump may still own the call
}

type admittedRecv[Req any] struct {
	msg *Req
	err error
}

func newAdmittedHTTPStream[Req, Resp any](stream grpc.BidiStreamingServer[Req, Resp], ctx context.Context) *admittedHTTPStream[Req, Resp] {
	return &admittedHTTPStream[Req, Resp]{BidiStreamingServer: stream, ctx: ctx}
}

func (s *admittedHTTPStream[Req, Resp]) Context() context.Context { return s.ctx }

func (s *admittedHTTPStream[Req, Resp]) canceled() error {
	return status.FromContextError(s.ctx.Err()).Err()
}

// Recv returns the next message, or the drain error as soon as the permit
// context ends. The pump calls the stream's Recv only when asked, so nothing
// is read ahead. Like grpc's own Recv it must not be called concurrently.
func (s *admittedHTTPStream[Req, Resp]) Recv() (*Req, error) {
	if s.recvStuck || s.ctx.Err() != nil {
		return nil, s.canceled()
	}
	s.recvOnce.Do(func() {
		s.recvAsk = make(chan struct{})
		s.recvDone = make(chan admittedRecv[Req], 1)
		go s.recvPump()
	})
	select {
	case <-s.ctx.Done():
		return nil, s.canceled()
	case s.recvAsk <- struct{}{}:
	}
	select {
	case <-s.ctx.Done():
		// A result that landed as the drain fired was already consumed
		// from the stream; hand it back rather than drop it.
		select {
		case r := <-s.recvDone:
			return r.msg, r.err
		default:
		}
		s.recvStuck = true
		return nil, s.canceled()
	case r := <-s.recvDone:
		return r.msg, r.err
	}
}

func (s *admittedHTTPStream[Req, Resp]) recvPump() {
	for {
		select {
		case <-s.recvAsk:
			msg, err := s.BidiStreamingServer.Recv()
			s.recvDone <- admittedRecv[Req]{msg, err}
		case <-s.ctx.Done():
			return
		}
	}
}

// Send sends msg, or returns the drain error as soon as the permit context
// ends. Like grpc's own Send it must not be called concurrently.
func (s *admittedHTTPStream[Req, Resp]) Send(msg *Resp) error {
	if s.sendStuck || s.ctx.Err() != nil {
		return s.canceled()
	}
	s.sendOnce.Do(func() {
		s.sendAsk = make(chan *Resp)
		s.sendDone = make(chan error, 1)
		go s.sendPump()
	})
	select {
	case <-s.ctx.Done():
		return s.canceled()
	case s.sendAsk <- msg:
	}
	select {
	case <-s.ctx.Done():
		select {
		case err := <-s.sendDone:
			return err
		default:
		}
		s.sendStuck = true
		return s.canceled()
	case err := <-s.sendDone:
		return err
	}
}

func (s *admittedHTTPStream[Req, Resp]) sendPump() {
	for {
		select {
		case msg := <-s.sendAsk:
			s.sendDone <- s.BidiStreamingServer.Send(msg)
		case <-s.ctx.Done():
			return
		}
	}
}
