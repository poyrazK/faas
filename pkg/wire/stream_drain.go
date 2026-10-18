package wire

import (
	"context"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StreamDrain ends a gRPC server's long-lived streams when its daemon begins
// shutting down. grpc.Server.GracefulStop waits for every open RPC, and peers
// hold watch streams (warm hints, log follows, capacity reports) open
// indefinitely, so without it every stop spent its whole grace before the
// replacement could start, while callers waited
// (docs/adr/20261018074719-zero-downtime-daemon-restarts.md).
//
// Ended streams return codes.Unavailable, the status clients already treat
// as "reconnect"; unary RPCs still finish within the grace.
type StreamDrain struct {
	once sync.Once
	ch   chan struct{}
}

// NewStreamDrain returns a drain that has not begun.
func NewStreamDrain() *StreamDrain {
	return &StreamDrain{ch: make(chan struct{})}
}

// Begin ends every stream the interceptor wraps, now and from here on.
// Call it before GracefulStop. Idempotent.
func (d *StreamDrain) Begin() {
	d.once.Do(func() { close(d.ch) })
}

// ServerOption installs the drain as a stream interceptor.
func (d *StreamDrain) ServerOption() grpc.ServerOption {
	return grpc.ChainStreamInterceptor(d.intercept)
}

func (d *StreamDrain) intercept(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx, cancel := context.WithCancel(ss.Context())
	defer cancel()
	go func() {
		select {
		case <-d.ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	err := handler(srv, &drainedStream{ServerStream: ss, ctx: ctx})
	select {
	case <-d.ch:
		if ss.Context().Err() == nil {
			return status.Error(codes.Unavailable, "server shutting down; reconnect")
		}
	default:
	}
	return err
}

type drainedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *drainedStream) Context() context.Context { return s.ctx }
