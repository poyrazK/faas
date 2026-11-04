package vmmdgrpc

// Sandbox session calls (ADR-20261027090013 §4). gatewayd-internal sends one
// sandboxproto operation per SandboxCall: vmmd checks the request, marks the
// instance active so it is not quiet-paused mid-operation (ADR-1151), thaws it
// if it is frozen and resumes it if it is quiet-paused, dials the guest's sandbox port and relays frames
// both ways until the guest's result or error. vmmd parses no guest
// filesystem; it relays frames.

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// sandboxSessions is the fcvm capability SandboxCall needs.
type sandboxSessions interface {
	DialSandboxSession(ctx context.Context, instance, sandboxID string) (net.Conn, error)
}

// hostSandboxFrames are the frames a caller may send after the request.
var hostSandboxFrames = map[uint32]bool{
	sandboxproto.FrameData: true, sandboxproto.FrameDataEnd: true, sandboxproto.FrameStdin: true,
	sandboxproto.FrameStdinEOF: true, sandboxproto.FrameResize: true, sandboxproto.FrameKill: true,
}

// SandboxCall runs one sandbox session operation.
func (s *Server) SandboxCall(stream vmmdpb.Vmmd_SandboxCallServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil || start.GetInstance() == "" || start.GetSandboxId() == "" {
		return status.Error(codes.InvalidArgument, "the first frame must name the instance and the sandbox")
	}
	var req sandboxproto.Request
	if err := sandboxproto.DecodeStrict(start.GetRequest(), &req); err != nil {
		return status.Error(codes.InvalidArgument, "the sandbox request is not valid JSON")
	}
	if err := req.Validate(); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	sessions, ok := s.vmm.(sandboxSessions)
	if !ok {
		return status.Error(codes.Unimplemented, "this vmmd serves no sandbox sessions")
	}
	ctx := stream.Context()
	instance := start.GetInstance()
	s.beginActivity(instance)
	defer s.endActivity(instance)
	// A session VM RunSandbox keeps reports its calls and is thawed in place;
	// an instance it does not keep is not a session VM, which the dial
	// refuses below.
	if act := s.sandboxActivityFor(instance); act != nil {
		act.begin()
		defer func() { act.end(time.Now()) }()
		if err := s.thawSandbox(ctx, instance, act); err != nil {
			s.log.Warn("sandbox: thaw failed", "instance", instance, "err", err)
			return status.Error(codes.Unavailable, "the sandbox could not be thawed; retry")
		}
	}
	if err := s.prepareGuest(ctx, instance); err != nil {
		return status.Error(codes.Unavailable, "the sandbox is frozen or resuming; retry")
	}
	conn, err := sessions.DialSandboxSession(ctx, instance, start.GetSandboxId())
	if errors.Is(err, fcvm.ErrSandboxNotFound) {
		return status.Error(codes.NotFound, "no such sandbox session")
	}
	if err != nil {
		return status.Error(codes.Unavailable, "the sandbox session is unreachable")
	}
	guest := sandboxproto.NewConn(conn)
	defer func() { _ = guest.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = guest.Close() })
	defer stop()
	if err := guest.WriteJSON(ctx, sandboxproto.FrameRequest, req); err != nil {
		return status.Error(codes.Unavailable, "the sandbox session closed")
	}
	go relayToGuest(ctx, stream, guest)
	return relayFromGuest(ctx, stream, guest)
}

// relayToGuest forwards the caller's input frames until its stream ends.
func relayToGuest(ctx context.Context, stream vmmdpb.Vmmd_SandboxCallServer, guest *sandboxproto.Conn) {
	for {
		msg, err := stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				_ = guest.Close()
			}
			return
		}
		f := msg.GetFrame()
		if f == nil || !hostSandboxFrames[f.GetType()] {
			continue
		}
		if err := guest.Write(ctx, f.GetType(), f.GetBody()); err != nil {
			return
		}
	}
}

// relayFromGuest forwards the guest's frames until its result or error.
func relayFromGuest(ctx context.Context, stream vmmdpb.Vmmd_SandboxCallServer, guest *sandboxproto.Conn) error {
	for {
		frameType, body, err := guest.Read(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return status.FromContextError(ctxErr).Err()
			}
			return status.Error(codes.Unavailable, "the sandbox session closed before answering")
		}
		if err := stream.Send(&vmmdpb.SandboxCallEvent{Frame: &vmmdpb.SandboxFrame{Type: frameType, Body: body}}); err != nil {
			return err
		}
		if frameType == sandboxproto.FrameResult || frameType == sandboxproto.FrameError {
			return nil
		}
	}
}
