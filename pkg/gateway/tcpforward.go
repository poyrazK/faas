package gateway

// TCP forwarding is deliberately separate from the HTTP reverse proxy. A
// public Layer-4 listener has no request headers to parse and must preserve
// half-closes and arbitrary application bytes (for example an SSH or
// PostgreSQL startup packet).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TCPForwarder connects one accepted public TCP socket to a guest listener
// through vmmd's protocol-neutral ForwardTCPStream RPC. The caller resolves
// the public endpoint to a Target before invoking ServeConn; this keeps the
// transport independent of the future tcpd listener and route store.
type TCPForwarder struct {
	Nodes NodeClientLookup
	// MaxBytes bounds each direction of a session. Zero uses the platform
	// default. A smaller value is useful for plan-specific admission.
	MaxBytes int64
}

// ServeConn forwards conn until either side closes or the stream fails. It
// returns a gRPC status for transport failures so tcpd can distinguish a
// missing compute node from a guest-side close in its metrics.
func (f TCPForwarder) ServeConn(ctx context.Context, conn net.Conn, target Target) error {
	if f.Nodes == nil {
		return status.Error(codes.FailedPrecondition, "TCP forwarder has no node lookup")
	}
	if conn == nil {
		return status.Error(codes.InvalidArgument, "TCP forwarder received a nil connection")
	}
	if target.Port < 0 || target.Port > 65535 {
		return status.Error(codes.InvalidArgument, "guest TCP port must be 0 or between 1 and 65535")
	}
	defer conn.Close()

	cli, closer, ok := f.Nodes.ClientFor(ctx, target.NodeID)
	if !ok || cli == nil {
		return status.Errorf(codes.Unavailable, "compute node %q is unavailable", target.NodeID)
	}
	if closer != nil {
		defer closer.Close()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := cli.ForwardTCPStream(ctx)
	if err != nil {
		return status.Errorf(codes.Unavailable, "open TCP forward stream: %v", err)
	}
	maxBytes := f.MaxBytes
	if maxBytes <= 0 || maxBytes > api.RawTCPStreamMaxBytes {
		maxBytes = api.RawTCPStreamMaxBytes
	}
	if err := stream.Send(&vmmdpb.ForwardTCPRequest{
		Frame: &vmmdpb.ForwardTCPRequest_Init{Init: &vmmdpb.ForwardTCPRequestInit{
			Instance: target.InstanceID,
			Port:     uint32(target.Port),
			MaxBytes: maxBytes,
		}},
	}); err != nil {
		return status.Errorf(codes.Unavailable, "send TCP forward init: %v", err)
	}

	results := make(chan tcpDirectionResult, 2)
	go func() {
		results <- tcpDirectionResult{side: tcpDirectionSend, err: tcpConnToStream(ctx, conn, stream, maxBytes)}
	}()
	go func() {
		results <- tcpDirectionResult{side: tcpDirectionReceive, err: tcpStreamToConn(conn, stream, maxBytes)}
	}()

	first := <-results
	var second tcpDirectionResult
	if first.side == tcpDirectionSend && isCleanTCPShutdown(first.err) {
		// Client half-close is not the end of a TCP session: the guest may
		// still be sending its final response. Keep receiving until vmmd
		// closes its half of the stream.
		second = <-results
	} else {
		// The guest closed first, or one direction failed. Close the public
		// socket to unblock the remaining copy goroutine.
		cancel()
		_ = conn.Close()
		second = <-results
	}
	cancel()
	_ = conn.Close()
	if !isCleanTCPShutdown(first.err) {
		return first.err
	}
	if !isCleanTCPShutdown(second.err) {
		return second.err
	}
	return nil
}

const (
	tcpDirectionSend    = "send"
	tcpDirectionReceive = "receive"
)

type tcpDirectionResult struct {
	side string
	err  error
}

func tcpConnToStream(ctx context.Context, conn net.Conn, stream grpc.BidiStreamingClient[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], maxBytes int64) error {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > maxBytes {
				return status.Errorf(codes.ResourceExhausted, "TCP request exceeded %d bytes", maxBytes)
			}
			chunk := append([]byte(nil), buf[:n]...)
			if sendErr := stream.Send(&vmmdpb.ForwardTCPRequest{
				Frame: &vmmdpb.ForwardTCPRequest_BodyChunk{BodyChunk: chunk},
			}); sendErr != nil {
				return status.Errorf(codes.Unavailable, "send TCP request bytes: %v", sendErr)
			}
		}
		if errors.Is(err, io.EOF) {
			if closeErr := stream.CloseSend(); closeErr != nil {
				return status.Errorf(codes.Unavailable, "half-close TCP forward stream: %v", closeErr)
			}
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read public TCP connection: %w", err)
		}
	}
}

func tcpStreamToConn(conn net.Conn, stream grpc.BidiStreamingClient[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], maxBytes int64) error {
	first := true
	var total int64
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if first {
				return status.Error(codes.Internal, "TCP forward stream ended before response init")
			}
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Unavailable, "receive TCP response bytes: %v", err)
		}
		if first {
			first = false
			init := frame.GetInit()
			if init == nil {
				return status.Error(codes.Internal, "TCP forward stream omitted response init")
			}
			if init.GetError() != "" {
				return status.Errorf(codes.Unavailable, "guest TCP listener unavailable: %s", init.GetError())
			}
			continue
		}
		chunk := frame.GetBodyChunk()
		if len(chunk) == 0 {
			continue
		}
		total += int64(len(chunk))
		if total > maxBytes {
			return status.Errorf(codes.ResourceExhausted, "TCP response exceeded %d bytes", maxBytes)
		}
		if _, err := conn.Write(chunk); err != nil {
			return fmt.Errorf("write public TCP connection: %w", err)
		}
	}
}

func isCleanTCPShutdown(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.Canceled
	}
	return false
}
