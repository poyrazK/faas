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

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
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
	serveDone  chan struct{}
	sink       *egresssink.EgressSink
	log        *slog.Logger
}

// chmodSocket applies mode to a unix-socket path, retrying briefly
// when the path is briefly unresolvable. The kernel publishes the
// dirent asynchronously after net.Listen returns; under tmpfs load
// (CI runner pool, observed cycle 13 of a 16-cycle restart loop) the
// publish can lag tens of ms past the listen return, so the next
// syscall sees ENOENT. Retry up to 500ms before failing the start —
// production callers always reach start() during boot when the
// listener has never existed, so a single ENOENT is expected; the
// 16-cycle CI test surfaced the long tail.
func chmodSocket(path string, mode os.FileMode) error {
	deadline := time.Now().Add(500 * time.Millisecond)
	var lastErr error
	for {
		if err := os.Chmod(path, mode); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForSocketPath blocks until os.Stat on the unix-socket path
// succeeds, retrying while ENOENT. Mirrors the chmodSocket retry
// shape — the production code at start() net.Listen's, then
// immediately chmods (chmodSocket retries on ENOENT) AND waits
// here so any caller that immediately dials sees a stable surface.
// The 2s budget is matched to the test's outer wait in
// TestEgressStopStopStart_RepeatedCycle; on a loaded CI runner the
// kernel's dirent publish can lag tens of ms after net.Listen
// returns, and that lag is amplified under tight restart loops
// (cycle 12 of 16 surfaced this in PR #703 run 31122020494).
// 2s keeps worst-case startup latency bounded while still
// tolerating the cumulative tmpfs pressure a daemon sees under
// real ops restarts.
func waitForSocketPath(path string, deadline time.Duration) error {
	end := time.Now().Add(deadline)
	var lastErr error
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else {
			lastErr = err
		}
		if time.Now().After(end) {
			return lastErr
		}
		time.Sleep(5 * time.Millisecond)
	}
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
	egresspb.RegisterEgressTxServiceServer(gs, srv)
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
		// The kernel publishes the dirent asynchronously after
		// net.Listen returns. Under tmpfs load (CI runner pool,
		// observed in TestEgressStopStopStart_RepeatedCycle —
		// cycle 2 of 16 in run 31080218226, cycle 12 of 16 in
		// PR #703 run 31122020494), the publish can lag tens of
		// ms past the listen return and that lag is amplified
		// under tight restart loops. Any caller that immediately
		// dials or chmods hits ENOENT. Wait until the dirent is
		// visible so callers see a stable surface. The subsequent
		// chmodSocket call also retries on ENOENT, but waiting
		// here shortens the failure mode for callers that don't
		// retry. The 2s budget matches the test's outer
		// waitForSocket in TestEgressStopStopStart_RepeatedCycle.
		if err := waitForSocketPath(l.socketPath, 2*time.Second); err != nil {
			_ = lis.Close()
			return fmt.Errorf("gatewayd egress: wait for socket dirent: %w", err)
		}
		if err := chmodSocket(l.socketPath, egressGRPCSocketMode); err != nil {
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
	l.serveDone = make(chan struct{})
	go func() {
		defer close(l.serveDone)
		if err := l.server.Serve(lis); err != nil {
			l.log.Warn("gatewayd egress: serve", "err", err)
		}
	}()
	return nil
}

// stop tears down the gRPC server. The socket file is NOT
// removed here: a stop-time Remove races against the next
// start()'s os.Remove→net.Listen sequence, and on systemd
// Restart=on-failure the old daemon's deferred Remove has
// been observed to fire AFTER the new daemon's net.Listen
// succeeded, deleting a live dirent that meterd then dials
// into nothing (cd-controlplane run 31121004495 — meterd
// OOM-killed from "stream open failed" log-flood because
// /run/faas/gatewayd-egress.sock kept disappearing).
//
// Stale dirents from a crash (where neither Remove nor the
// graceful-close path ran) are handled by start()'s
// pre-Remove — that one runs against a known-gone listener
// and can't race with the next process. This mirrors
// pkg/gateway/synth.go:SynthServer.Stop, which only calls
// http.Server.Shutdown and never unlinks.
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
	if l.serveDone != nil {
		<-l.serveDone
	}
	return nil
}
