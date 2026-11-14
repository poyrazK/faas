// Package apidgrpc holds the typed client surface gatewayd-internal
// uses to talk to apid's unix-socket gRPC server (ADR-070 / ADR-096).
//
// Today the only service is AppErrors (cmd/apid/v1/app_errors.proto),
// added so the gatewayd-internal error recorder can ship per-request
// error records to apid without opening a direct Postgres connection.
// apid is the sole writer to app_errors / app_error_requests
// (CLAUDE.md ownership); gatewayd-internal is forbidden from importing
// pkg/state's Postgres path for this store.
//
// Direction note: gatewayd-internal → apid ONLY. apid never dials this
// service (the depguard apid-control-plane-only deny list prevents apid
// from importing pkg/vmmdgrpc or pkg/apidgrpc — apid is the server).
package apidgrpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

// AppErrorsClient is the gatewayd-internal-side interface the
// recorder holds. The interface is the union of every gRPC call
// gatewayd-internal makes against apid's AppErrors service. Each
// method maps 1:1 to a method on *Client so unit tests can
// substitute a fake without spinning a real gRPC dial.
//
// Future additions go HERE first — the interface is the canonical
// surface; *Client is the production implementation; tests are
// expected to provide fakes that satisfy the interface.
type AppErrorsClient interface {
	// IncrementAppError opens a stream to apid and ships one
	// record per send. Each send yields one response carrying
	// the per-record outcome. The stream returns when the
	// caller invokes Close() OR when ctx is cancelled. A
	// per-record ResourceExhausted (transient DB failure) is
	// delivered as the response on that single record — the
	// stream continues (load-bearing: a single bad row MUST
	// NOT halt the batch).
	IncrementAppError(ctx context.Context) (AppErrorStream, error)
	Close() error
}

// AppErrorStream is the per-call handle returned by
// (*Client).IncrementAppError. The handle is NOT safe for
// concurrent use — one goroutine sends, one goroutine receives.
// See pkg/scheddgrpc.LogStream for the same single-threaded
// contract.
type AppErrorStream interface {
	// Send queues one record for transmission. Returns
	// io.EOF after the server has half-closed. A non-nil
	// error is terminal for the stream.
	Send(*apidpb.IncrementAppErrorRequest) error
	// Recv blocks for the next response. Returns io.EOF when
	// the server half-closes (every queued Send has been
	// answered).
	Recv() (*apidpb.IncrementAppErrorResponse, error)
	// CloseSend half-closes the request stream (tells the
	// server "no more records"). Safe to call multiple times.
	CloseSend() error
}

// Client is the production implementation of AppErrorsClient.
// It owns the lazy gRPC connection to apid's unix socket.
type Client struct {
	conn *grpc.ClientConn
	cli  apidpb.AppErrorsClient
}

// compile-time assertion that *Client satisfies AppErrorsClient.
var _ AppErrorsClient = (*Client)(nil)

// Dial opens a lazy gRPC connection to apid's AppErrors endpoint. The
// legacy bare path remains a Unix socket authenticated by DAC; tcp:// and
// dns:// targets are rejected unless DialContext receives a complete mTLS
// config. Connection dials on the first RPC; Dial never blocks on apid being
// up.
//
// Legacy entrypoint retained for source compatibility; production
// code should call DialContext so the caller's context controls
// the dial. Mirrors the scheddgrpc.Dial vs DialContext split
// (issue #95).
func Dial(socketPath string) (*Client, error) {
	return DialContext(context.Background(), socketPath, nil)
}

// DialContext opens a lazy gRPC connection to apid. tlsCfg is
// required for tcp/dns targets; nil is fine for the single-box
// unix default. Wire layer performs the mTLS gating — see
// pkg/wire.DialContext.
func DialContext(ctx context.Context, target string, tlsCfg *tls.Config) (*Client, error) {
	if target == "" {
		return nil, errors.New("apidgrpc: empty apid target")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial apid %q: %w", target, err)
	}
	return &Client{conn: conn, cli: apidpb.NewAppErrorsClient(conn)}, nil
}

// NewClient wraps an already-dialed connection (used by bufconn
// tests in pkg/apidgrpc/app_errors_client_test.go).
func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, cli: apidpb.NewAppErrorsClient(conn)}
}

// IncrementAppError opens a new server-streaming RPC and returns
// a typed handle. The handle owns one in-flight RPC; closing it
// (via CloseSend or ctx cancel) ends the RPC. Callers should
// drain Recv() in a goroutine if they want to send + receive
// concurrently (same pattern as grpc.ClientStream).
func (c *Client) IncrementAppError(ctx context.Context) (AppErrorStream, error) {
	stream, err := c.cli.IncrementAppError(ctx)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: IncrementAppError: %w", err)
	}
	return &appErrorStream{stream: stream}, nil
}

// RecordHTTPIssue retries an Issues occurrence without replaying legacy counts.
func (c *Client) RecordHTTPIssue(ctx context.Context, req *apidpb.IncrementAppErrorRequest) (*apidpb.IncrementAppErrorResponse, error) {
	return c.cli.RecordHTTPIssue(ctx, req)
}

// Close shuts down the underlying gRPC connection. Safe to call
// multiple times; subsequent calls are no-ops.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// appErrorStream is the concrete AppErrorStream wrapping a
// grpc.ClientStream. The stream is single-threaded — Send and
// Recv are NOT safe to call from different goroutines
// concurrently. Callers that need concurrent use must coordinate
// externally.
type appErrorStream struct {
	stream apidpb.AppErrors_IncrementAppErrorClient
}

// Send forwards to the underlying grpc.ClientStream. The error
// from Send carries the gRPC status code when the stream is
// half-closed by the server.
func (s *appErrorStream) Send(req *apidpb.IncrementAppErrorRequest) error {
	if s == nil || s.stream == nil {
		return errors.New("apidgrpc: nil stream")
	}
	return s.stream.Send(req)
}

// Recv blocks for the next response. io.EOF is returned when the
// server half-closes.
func (s *appErrorStream) Recv() (*apidpb.IncrementAppErrorResponse, error) {
	if s == nil || s.stream == nil {
		return nil, errors.New("apidgrpc: nil stream")
	}
	return s.stream.Recv()
}

// CloseSend half-closes the request stream.
func (s *appErrorStream) CloseSend() error {
	if s == nil || s.stream == nil {
		return nil
	}
	return s.stream.CloseSend()
}
