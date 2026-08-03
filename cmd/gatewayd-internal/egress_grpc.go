// cmd/gatewayd egress-grpc listener (ADR-046 PR-2 producer channel).
//
// Why this lives in a separate file from main.go: the dialer,
// listener management, and registration logic are small but
// not zero; isolating them keeps main.go's runWithDeps readable
// and gives the test harness a single seam to swap the
// underlying grpc.Server transport.
//
// Why a second unix socket rather than sharing the synth one:
//   A unix socket can host either an HTTP server or a gRPC
//   server, not both. The synth service (pkg/gateway/synth.go)
//   is HTTP-shaped because the cron body is JSON over HTTP/1.1
//   (a simpler wire than gRPC frame encoding for the
//   one-shot-per-tick cron dispatch path). The egress service
//   (pkg/gateway/egressgrpc) is gRPC-shaped because the
//   producer/consumer relationship is long-lived and the
//   server-streaming RPC matches meterd's natural cadence
//   exactly. Separate sockets, identical DAC auth posture
//   (group `faas`, mode 0660 — ADR-015).

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"

	gatewaydpb "github.com/onebox-faas/faas/api/proto/onebox/faas/gatewayd/v1"
	"github.com/onebox-faas/faas/pkg/gateway/egressgrpc"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	// defaultEgressGRPCSocketPath is the default unix-domain socket
	// ADR-046 PR-2 gRPC producer channel listens on. Override with
	// FAAS_GATEWAY_EGRESS_SOCKET. Distinct from
	// FAAS_GATEWAY_SYNTH_SOCKET so the existing cron/dispatch
	// service stays HTTP-shaped on its own socket (see file
	// header).
	defaultEgressGRPCSocketPath = "/run/faas/gatewayd-egress.sock"

	// egressGRPCSocketMode mirrors the synth socket's 0660 + group
	// `faas` posture (ADR-015). Only schedd/meterd are in the
	// group, so the socket itself IS the auth.
	egressGRPCSocketMode = 0o660
)

// egressGRPCSocketPath returns the socket path to bind, honoring
// the FAAS_GATEWAY_EGRESS_SOCKET override. Empty string disables
// the listener entirely (used by tests + the e2e harness).
func egressGRPCSocketPath() string {
	if v, ok := os.LookupEnv("FAAS_GATEWAY_EGRESS_SOCKET"); ok && v != "" {
		return v
	}
	return defaultEgressGRPCSocketPath
}

// egressGRPCListener owns the *grpc.Server + its bound unix
// socket. Lifetime is bound to the cmd/gatewayd daemon: start()
// binds + serves; stop() shuts down the server with a 5-second
// grace and removes the socket file (the daemon owns the
// socket, recreate is safer than fail-on-EADDRINUSE at next
// boot).
type egressGRPCListener struct {
	socketPath string
	server     *grpc.Server
	listener   net.Listener
	sink       *egresssink.EgressSink
	log        *slog.Logger
}

// newEgressGRPCListener constructs (but does not start) the
// gRPC listener. target is the bind target (unix:///path or
// tcp://host:port for multi-box). tlsCfg is the server-side
// mTLS material for tcp targets; nil for unix-socket (single-box)
// targets. Empty target = noop listener (Start/Stop are both
// no-ops).
func newEgressGRPCListener(target string, tlsCfg *tls.Config, srv *egressgrpc.Server, log *slog.Logger) *egressGRPCListener {
	if target == "" {
		return &egressGRPCListener{log: log}
	}
	// Build a single varargs ServerOption list so the unix/
	// multi-box branches share the same limits + dial timeout.
	// Multi-box tcp targets need ServerCredsOrEmpty so gRPC's
	// transport does the TLS handshake — this is what populates
	// peer.AuthInfo with credentials.TLSInfo for the
	// handler-layer CN binding (ADR-052).
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(1 << 20), // 1 MiB; one frame is small
		grpc.MaxSendMsgSize(1 << 20), // matches above
		grpc.ConnectionTimeout(5 * time.Second),
	}
	if !isUnixSocketPath(target) {
		opts = append(opts, wire.ServerCredsOrEmpty(tlsCfg)...)
	}
	gs := grpc.NewServer(opts...)
	gatewaydpb.RegisterEgressTxServiceServer(gs, srv)
	return &egressGRPCListener{
		socketPath: target,
		server:     gs,
		sink:       nil, // reserved for a future /debug endpoint
		log:        log,
	}
}

// isUnixSocketPath detects unix:// scheme OR a bare filesystem
// path. Used by the egress listener to decide whether to require
// a non-nil tlsCfg.
func isUnixSocketPath(target string) bool {
	if len(target) >= 7 && target[:7] == "unix://" {
		return true
	}
	if len(target) > 0 && target[0] == '/' {
		return true
	}
	return false
}

// start binds the unix socket (or tcp port) and starts the gRPC
// server in a goroutine. Unix sockets get the 0660 + group `faas`
// chmod after bind (ADR-015). TCP targets skip the chmod and
// rely on the TLS handshake for auth.
//
// ctx bounds the wire.Listen call on the TCP branch — unix sockets
// don't need it (net.Listen is synchronous and never blocks past
// the bind). The ctx isn't propagated to the running server because
// the caller owns the shutdown ctx via stop(ctx); the runWithDeps
// shutdown path uses a separate, deadline-bounded context for that.
func (l *egressGRPCListener) start(ctx context.Context) error {
	if l == nil || l.socketPath == "" || l.server == nil {
		return nil
	}
	var lis net.Listener
	var err error
	if isUnixSocketPath(l.socketPath) {
		if err := os.Remove(l.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("gatewayd egress: remove stale socket: %w", err)
		}
		lis, err = net.Listen("unix", l.socketPath)
		if err != nil {
			return fmt.Errorf("gatewayd egress: listen %s: %w", l.socketPath, err)
		}
		if err := os.Chmod(l.socketPath, egressGRPCSocketMode); err != nil {
			_ = lis.Close()
			return fmt.Errorf("gatewayd egress: chmod: %w", err)
		}
		l.listener = lis
		l.log.Info("gatewayd egress: listening", "socket", l.socketPath)
	} else {
		// TCP/DNS target. Use wire.Listen so the listener is
		// raw TCP — gRPC's transport will do the TLS handshake
		// via ServerCreds passed to grpc.NewServer above
		// (ADR-052 §Handler-layer peer binding).
		lis, err = wire.Listen(ctx, l.socketPath, nil)
		if err != nil {
			return fmt.Errorf("gatewayd egress: listen %s: %w", l.socketPath, err)
		}
		l.listener = lis
		l.log.Info("gatewayd egress: listening", "addr", l.socketPath)
	}
	go func() {
		if err := l.server.Serve(lis); err != nil {
			l.log.Warn("gatewayd egress: serve", "err", err)
		}
	}()
	return nil
}

// stop tears down the gRPC server + removes the socket file.
// The returned error is informational; the daemon continues
// shutdown even on cleanup failures.
//
// ctx bounds the graceful shutdown window: GracefulStop blocks
// until in-flight RPCs drain, which can be slow under load; if
// ctx fires first we escalate to Stop() (immediate connection
// close) so the daemon doesn't outlive its shutdown deadline.
// The deferred caller at runWithDeps wraps this with a 5-second
// timeout — without the ctx race, that 5s grace was silently
// violated under load (GracefulStop would block forever).
func (l *egressGRPCListener) stop(ctx context.Context) error {
	if l == nil || l.server == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		l.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		// Graceful shutdown completed within ctx.
	case <-ctx.Done():
		// Deadline blew past; force-close so the daemon doesn't
		// hold the process open. gRPC's Stop returns immediately.
		l.server.Stop()
	}
	if l.socketPath != "" && isUnixSocketPath(l.socketPath) {
		_ = os.Remove(l.socketPath)
	}
	return nil
}
