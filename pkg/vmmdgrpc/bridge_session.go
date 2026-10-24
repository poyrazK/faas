package vmmdgrpc

// Raw, TCP and UDP connections as sessions on the instance's bridge
// (connection-survival ADR §2, phase P1b). With FAAS_INSTANCE_BRIDGE_SESSIONS=1
// each such connection is one unix stream connection to the instance's
// persistent bridge instead of a process spawned for it (nsenter, a setns
// and an exec per connect). A session looks to the forward handlers like
// the per-connection process did: request bytes in, response bytes out
// until EOF, and Wait reporting a clean or failed end. The process path
// stays the fallback and the default.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/instancebridge"
)

const instanceBridgeSessionsEnv = "FAAS_INSTANCE_BRIDGE_SESSIONS"

// instanceBridgeSessionsEnabled reports whether raw, TCP and UDP
// connections use the persistent bridge, which needs the v2 bridge.
func instanceBridgeSessionsEnabled() bool {
	return os.Getenv(instanceBridgeSessionsEnv) == "1" && currentStreamBridgeVersion() == "v2" && persistentStreamBridgeEnabled()
}

// errSessionsUnsupported is a bridge that does not speak the session
// protocol; the connection falls back to its own process.
var errSessionsUnsupported = errors.New("instance bridge does not serve sessions")

// bridgeWaiter ends a bridge connection: Wait is nil for a clean end.
type bridgeWaiter interface{ Wait() error }

// bridgeConn is one raw, TCP or UDP connection to the guest: a session
// on the instance bridge or a per-connection bridge process.
type bridgeConn struct {
	run    bridgeWaiter
	kill   func()
	input  io.WriteCloser // Close ends the request
	output io.Reader      // EOF at the bridge's end, clean or not
	stderr func() string
	close  func()
}

// bridgeFailed reports a bridge that ended in failure: a process's
// nonzero exit or a session's failed end.
func bridgeFailed(err error) bool {
	var exitErr *exec.ExitError
	var sessionErr *instancebridge.SessionError
	return errors.As(err, &exitErr) || errors.As(err, &sessionErr)
}

// openBridgeConn opens a kind connection to the guest's port: a session
// when sessions are on, else (or when the bridge cannot serve one) a
// per-connection process.
func (s *Server) openBridgeConn(ctx context.Context, kind, instance, netnsName string, id bridgeIdentity, pid int, port uint32) (*bridgeConn, error) {
	if instanceBridgeSessionsEnabled() && s.streamBridges != nil {
		conn, err := s.openBridgeSession(ctx, kind, instance, netnsName, id, port)
		if !errors.Is(err, errSessionsUnsupported) {
			return conn, err
		}
		s.log.Warn("vmmd: instance bridge served no session; using a bridge process", "instance", instance, "kind", kind)
	}
	return openBridgeProcess(ctx, kind, id, pid, port)
}

func openBridgeProcess(ctx context.Context, kind string, id bridgeIdentity, pid int, port uint32) (*bridgeConn, error) {
	if kind == instancebridge.KindRaw {
		cmd, stdinR, stdinW, stdoutR, stderr, err := rawBridgeSpawn(ctx, id, pid, port)
		if err != nil {
			return nil, err
		}
		return processConn(cmd, stdinW, stdoutR, stderr.String, stdinR, stdinW, stdoutR), nil
	}
	pathEnv, path := tcpBridgePathEnv, vmmdTCPBridgePath
	if kind == instancebridge.KindUDP {
		pathEnv, path = udpBridgePathEnv, vmmdUDPBridgePath
	}
	cmd, stdinW, stdoutR, readyR, stderr, err := namespaceBridgeSpawn(ctx, id, pid, port, pathEnv, path)
	if err != nil {
		return nil, err
	}
	return processConn(cmd, stdinW, stdoutR, stderr.String, stdinW, stdoutR, readyR), nil
}

func processConn(cmd *exec.Cmd, input *os.File, output *os.File, stderr func() string, files ...*os.File) *bridgeConn {
	return &bridgeConn{
		run:    cmd,
		kill:   func() { _ = cmd.Process.Kill() },
		input:  input,
		output: output,
		stderr: stderr,
		close:  func() { closeFiles(files...) },
	}
}

// openBridgeSession leases the instance's bridge and starts a session.
// A raw session whose guest dial failed is returned already ended, with
// the dial error as its stderr, as the raw process reported it; TCP and
// UDP fail the open, as their process readiness did.
func (s *Server) openBridgeSession(ctx context.Context, kind, instance, netnsName string, id bridgeIdentity, port uint32) (*bridgeConn, error) {
	lease, err := s.streamBridges.acquireSession(ctx, instance, netnsName, id, port)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "instance bridge: %v", err)
	}
	session, err := dialBridgeSession(ctx, lease.entry.socket, id, kind, port)
	if err != nil {
		lease.release()
		return nil, err
	}
	session.lease = lease
	return session.conn(), nil
}

// bridgeSession is a session on a bridge's data socket.
type bridgeSession struct {
	sock    *net.UnixConn
	frames  *instancebridge.FrameReader
	lease   *streamBridgeLease
	stopCtx func() bool

	// readMu serializes reads: a handler's deferred Wait drains the
	// session while its response goroutine may still be reading it.
	readMu  sync.Mutex
	mu      sync.Mutex
	ended   bool
	result  error
	message string
	once    sync.Once
}

func dialBridgeSession(ctx context.Context, socket string, id bridgeIdentity, kind string, port uint32) (*bridgeSession, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "instance bridge dial: %v", err)
	}
	sock, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, status.Error(codes.Internal, "instance bridge socket is not a unix connection")
	}
	if err := id.verifyBridgePeer(c); err != nil {
		_ = c.Close()
		return nil, status.Errorf(codes.Unavailable, "instance bridge peer: %v", err)
	}
	s := &bridgeSession{sock: sock}
	// The session context ends with the client or the instance (H9-129),
	// as it killed the per-connection process.
	s.stopCtx = context.AfterFunc(ctx, func() { _ = sock.Close() })
	if port == 0 || port > 65535 {
		s.release()
		return nil, status.Error(codes.InvalidArgument, "guest port must be between 1 and 65535")
	}
	if err := instancebridge.WriteHello(sock, kind, uint16(port)); err != nil {
		s.release()
		return nil, status.Errorf(codes.Unavailable, "instance bridge hello: %v", err)
	}
	r := bufio.NewReader(sock)
	err = instancebridge.ReadReady(r)
	var dial *instancebridge.DialError
	switch {
	case err == nil:
		s.frames = instancebridge.NewFrameReader(r)
		return s, nil
	case errors.As(err, &dial) && kind == instancebridge.KindRaw:
		s.end(&instancebridge.SessionError{Code: instancebridge.CodeDial, Message: dial.Message})
		return s, nil
	case errors.As(err, &dial):
		s.release()
		return nil, status.Errorf(codes.Unavailable, "guest socket dial failed: %s", dial.Message)
	case ctx.Err() == nil && !errors.Is(err, io.ErrUnexpectedEOF):
		// Not the session protocol: a bridge from before it.
		s.release()
		return nil, fmt.Errorf("%w: %w", errSessionsUnsupported, err)
	}
	s.release()
	return nil, status.Errorf(codes.Unavailable, "instance bridge ready: %v", err)
}

func (s *bridgeSession) conn() *bridgeConn {
	return &bridgeConn{
		run:    s,
		kill:   func() { _ = s.sock.Close() },
		input:  sessionInput{s.sock},
		output: s,
		stderr: s.stderr,
		close:  s.release,
	}
}

// Read returns the guest's bytes, then EOF at the session's end; the
// end's outcome is Wait's.
func (s *bridgeSession) Read(p []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	s.mu.Lock()
	ended := s.ended
	s.mu.Unlock()
	if ended || s.frames == nil {
		return 0, io.EOF
	}
	n, err := s.frames.Read(p)
	if err != nil {
		s.end(err)
		return n, io.EOF
	}
	return n, nil
}

// end records the session's outcome once: nil for a clean end frame.
func (s *bridgeSession) end(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	var sessionErr *instancebridge.SessionError
	switch {
	case errors.Is(err, io.EOF):
		return
	case errors.As(err, &sessionErr):
		s.result, s.message = sessionErr, sessionErr.Message
	default:
		s.result = &instancebridge.SessionError{Code: instancebridge.CodeIO, Message: "session ended without an end frame: " + err.Error()}
		s.message = s.result.Error()
	}
}

// Wait reads the rest of the bridge's output and returns the session's
// outcome, as a process's Wait returns its exit.
func (s *bridgeSession) Wait() error {
	_, _ = io.Copy(io.Discard, s)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func (s *bridgeSession) stderr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.message
}

func (s *bridgeSession) release() {
	s.once.Do(func() {
		if s.stopCtx != nil {
			s.stopCtx()
		}
		_ = s.sock.Close()
		if s.lease != nil {
			s.lease.release()
		}
	})
}

// sessionInput is the request side: Close half-closes it, the end of the
// request the guest sees.
type sessionInput struct{ sock *net.UnixConn }

func (i sessionInput) Write(p []byte) (int, error) { return i.sock.Write(p) }
func (i sessionInput) Close() error                { return i.sock.CloseWrite() }
