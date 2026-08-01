// apid_bridge.go — githubd-side dial to the apid gRPC bridge for
// per-app build enqueue (issue #432 phase 5).
//
// Direction: githubd → apid only. The githubd dispatcher dials
// /run/faas/apid-githubd.sock after the fan-out and stages each
// app's per-app RootDir subtree into githubd's build-sources dir
// as a per-app .tar.gz. The apid-side handler (cmd/apid/
// githubd_bridge.go) creates the deployment + build rows and emits
// the build_queued pg_notify that builderd LISTENs on.
//
// Auth: the unix-socket 0660/group-`faas` DAC is the only auth in
// v1.0 (ADR-015). The transport is insecure credentials over a
// trusted local path; see pkg/wire.DialContext. Multi-box mTLS
// work (ADR-052) raises the bar in a follow-up.
//
// The dial target is read from FAAS_APID_GITHUBD_BRIDGE_SOCK via
// the test seam (deps.getenv). Empty value disables the dial and
// falls back to the stub client — same pattern as the apid-side
// githubd_client.go stub. Tests disable by default so macOS dev
// boxes don't try to dial /run/faas (read-only on macOS).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
)

// ApidBridgeClient is the githubd-side view of the apid gRPC
// bridge. The interface exists so the dispatcher can be unit-
// tested with a stub without dialing a real socket. The single
// method (EnqueueBuild) is the load-bearing bridge — the apid-
// side githubd_bridge.go handler implements the corresponding
// server side.
//
// The interface deliberately mirrors the proto Request/Response
// shapes directly so the dispatcher-side code can pass the
// generated proto types without translation. The liveClient
// wrapper converts these to the gRPC stub-client invocation.
// Tests that want to bypass the proto dependency can stub the
// interface with a function-field fake.
type ApidBridgeClient interface {
	EnqueueBuild(ctx context.Context, in *githubdpb.EnqueueBuildRequest) (*githubdpb.EnqueueBuildResponse, error)
	Close() error
}

// errApidBridgeNotReady is the dispatcher-side sentinel returned
// by the stub when the bridge socket isn't configured. The
// dispatcher treats this as a known-fatal-by-design signal — log
// + skip the build enqueue, continue past the failure. The
// dispatch audit row records the skip reason so the operator
// can correlate webhook receipts with build enqueue state.
var errApidBridgeNotReady = errors.New("githubd: apid bridge not configured (set FAAS_APID_GITHUBD_BRIDGE_SOCK)")

// stubApidBridgeClient is the buffer-disabled default. Returns
// errApidBridgeNotReady for every EnqueueBuild so the dispatcher
// can log + skip without a real apid process running. Close() is
// a no-op so the cleanup paths are safe.
type stubApidBridgeClient struct{}

// EnqueueBuild returns the not-ready sentinel. Slice 1 PR replaces
// the production wiring with a live client.
func (stubApidBridgeClient) EnqueueBuild(context.Context, *githubdpb.EnqueueBuildRequest) (*githubdpb.EnqueueBuildResponse, error) {
	return nil, errApidBridgeNotReady
}

// Close is a no-op for the stub.
func (stubApidBridgeClient) Close() error { return nil }

// liveApidBridgeClient is the slice-1 production wrapper around
// the generated githubdpb.GithubdClient. The dial happens in
// newApidBridgeClient (the constructor below). Lives in this
// file so the dispatcher-side tests don't have to import the
// proto package directly — they only see the ApidBridgeClient
// interface.
type liveApidBridgeClient struct {
	c    githubdpb.GithubdClient
	conn *grpc.ClientConn
	log  *slog.Logger
}

// Close releases the underlying socket connection. The
// *grpc.ClientConn owns the socket; the generated client just
// dispatches through it. The Close call routes through the
// ClientConn, not the generated client (which has no Close
// method).
func (l *liveApidBridgeClient) Close() error {
	if l == nil || l.conn == nil {
		return nil
	}
	return l.conn.Close()
}

// EnqueueBuild passes through to githubdpb.GithubdClient.EnqueueBuild.
// Mirrors the cmd/apid/githubd_client.go liveClient pattern.
func (l *liveApidBridgeClient) EnqueueBuild(ctx context.Context, in *githubdpb.EnqueueBuildRequest) (*githubdpb.EnqueueBuildResponse, error) {
	return l.c.EnqueueBuild(ctx, in)
}

// newApidBridgeClient is the dial constructor. Returns the stub
// when sock is empty (matches the apid-side githubd_client.go
// pattern). Production wires defaultDeps.getenv to os.Getenv; the
// systemd unit stamps FAAS_APID_GITHUBD_BRIDGE_SOCK=
// /run/faas/apid-githubd.sock explicitly so the default doesn't
// matter in prod.
//
// ctx participates in the dial so the daemon's lifecycle
// cancellation closes the connection cleanly. tlsCfg is nil for
// the loopback UNIX socket. Remote-target dial + mTLS will be
// wired here in the follow-up that decouples the control plane
// (ADR-052).
func newApidBridgeClient(ctx context.Context, sock string, tlsCfg *tls.Config, log *slog.Logger) ApidBridgeClient {
	if sock == "" {
		if log != nil {
			log.Info("apid bridge socket not configured; using stub client (issue #432 phase 5 slice 1)")
		}
		return stubApidBridgeClient{}
	}
	conn, err := wire.DialContext(ctx, sock, tlsCfg)
	if err != nil {
		if log != nil {
			log.Error("apid bridge dial failed; falling back to stub", "sock", sock, "err", err)
		}
		// Returning the stub (not the error) matches the
		// apid-side pattern: a transient dial failure must not
		// stop the daemon. The dispatcher logs + skips the
		// affected build when the stub returns
		// errApidBridgeNotReady.
		return stubApidBridgeClient{}
	}
	if log != nil {
		log.Info("apid bridge connected", "sock", sock)
	}
	return &liveApidBridgeClient{
		c:    githubdpb.NewGithubdClient(conn),
		conn: conn,
		log:  log,
	}
}
