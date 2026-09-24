// Package apidgrpc — request_telemetry client (ADR-127 PR-B).
//
// PR-A shipped the table + the recorder/publisher but the gRPC
// channel between gatewayd-internal and apid was a log-only stub
// (cmd/gatewayd-internal/run.go:1758-1769). PR-B closes that
// gap: gatewayd-internal dials apid's RequestTelemetry service
// over the unix socket, ships collapsed buckets, and apid
// commits each row in its own transaction.
//
// The client surface mirrors AppErrorsClient (above) one-for-one
// — same dial pattern, same single-threaded stream contract, same
// pkg/wire.DialContext. The shape difference: RequestTelemetry
// has no dedupe-merge (the publisher already collapsed) and has
// a per-row RATE_LIMITED outcome the AppErrors service doesn't
// have (PR-B adds per-account rate caps on the ingest RPC; see
// pkg/api/limits.go::DebugTelemetryRequestsPerMinute).

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

// RequestTelemetryClient is the gatewayd-internal-side interface
// the publisher holds. The interface is the union of every gRPC
// call gatewayd-internal makes against apid's RequestTelemetry
// service. Each method maps 1:1 to a method on *Client so unit
// tests can substitute a fake without spinning a real gRPC dial.
type RequestTelemetryClient interface {
	// IncrementRequestTelemetry opens a stream to apid and ships
	// one record per send. Each send yields one response carrying
	// the per-record outcome. The stream returns when the caller
	// invokes Close() OR when ctx is cancelled. A per-record
	// rate-limit (RATE_LIMITED) or transient DB failure
	// (db_error) is delivered as the response on that single
	// record — the stream continues (load-bearing: a single
	// rate-limited row MUST NOT halt the batch).
	IncrementRequestTelemetry(ctx context.Context) (RequestTelemetryStream, error)
	Close() error
}

// RequestTelemetryStream is the per-call handle returned by
// (*Client).IncrementRequestTelemetry. The handle is NOT safe
// for concurrent use — one goroutine sends, one goroutine receives.
// Same single-threaded contract as AppErrorStream above.
type RequestTelemetryStream interface {
	// Send queues one record for transmission. Returns io.EOF
	// after the server has half-closed. A non-nil error is
	// terminal for the stream.
	Send(*apidpb.IncrementRequestTelemetryRequest) error
	// Recv blocks for the next response. Returns io.EOF when
	// the server half-closes (every queued Send has been
	// answered).
	Recv() (*apidpb.IncrementRequestTelemetryResponse, error)
	// CloseSend half-closes the request stream (tells the
	// server "no more records"). Safe to call multiple times.
	CloseSend() error
}

// RequestTelemetryClientImpl is the production implementation of
// RequestTelemetryClient. It owns the lazy gRPC connection to
// apid's unix socket. (Named RequestTelemetryClientImpl rather
// than Client to avoid collision with the AppErrors Client in
// the same package.)
type RequestTelemetryClientImpl struct {
	conn *grpc.ClientConn
	cli  apidpb.RequestTelemetryClient
}

// compile-time assertion that *RequestTelemetryClientImpl
// satisfies RequestTelemetryClient.
var _ RequestTelemetryClient = (*RequestTelemetryClientImpl)(nil)

// DialRequestTelemetry opens a lazy gRPC connection to apid's
// unix socket for the RequestTelemetry service. Same auth model
// as Dial above: socket DAC mode 0660 group `faas` is the only
// auth in v1.0; transport uses insecure credentials over a
// trusted local socket. Connection dials on first RPC; Dial
// never blocks on apid being up.
func DialRequestTelemetry(ctx context.Context, target string, tlsCfg *tls.Config) (*RequestTelemetryClientImpl, error) {
	if target == "" {
		return nil, errors.New("apidgrpc: empty apid target")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial apid %q for request telemetry: %w", target, err)
	}
	return &RequestTelemetryClientImpl{conn: conn, cli: apidpb.NewRequestTelemetryClient(conn)}, nil
}

// NewRequestTelemetryClient wraps an already-dialed connection
// (used by bufconn tests in pkg/apidgrpc/).
func NewRequestTelemetryClient(conn *grpc.ClientConn) *RequestTelemetryClientImpl {
	return &RequestTelemetryClientImpl{conn: conn, cli: apidpb.NewRequestTelemetryClient(conn)}
}

// IncrementRequestTelemetry opens a new server-streaming RPC
// and returns a typed handle. The handle owns one in-flight RPC;
// closing it (via CloseSend or ctx cancel) ends the RPC.
func (c *RequestTelemetryClientImpl) IncrementRequestTelemetry(ctx context.Context) (RequestTelemetryStream, error) {
	stream, err := c.cli.IncrementRequestTelemetry(ctx)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: IncrementRequestTelemetry: %w", err)
	}
	return &requestTelemetryStream{stream: stream}, nil
}

// RecordConsumerUsage is the durable accounting RPC. It is available even
// when the optional request debugger is disabled at the receiver.
func (c *RequestTelemetryClientImpl) RecordConsumerUsage(ctx context.Context, event *apidpb.ConsumerUsageEvent) (*apidpb.ConsumerUsageReceipt, error) {
	receipt, err := c.cli.RecordConsumerUsage(ctx, event)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: RecordConsumerUsage: %w", err)
	}
	return receipt, nil
}

// Close shuts down the underlying gRPC connection. Safe to call
// multiple times; subsequent calls are no-ops.
func (c *RequestTelemetryClientImpl) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// requestTelemetryStream is the concrete RequestTelemetryStream
// wrapping a grpc.ClientStream. Same single-threaded contract as
// appErrorStream above.
type requestTelemetryStream struct {
	stream apidpb.RequestTelemetry_IncrementRequestTelemetryClient
}

// Send forwards to the underlying grpc.ClientStream.
func (s *requestTelemetryStream) Send(req *apidpb.IncrementRequestTelemetryRequest) error {
	if s == nil || s.stream == nil {
		return errors.New("apidgrpc: nil stream")
	}
	return s.stream.Send(req)
}

// Recv blocks for the next response. io.EOF is returned when the
// server half-closes.
func (s *requestTelemetryStream) Recv() (*apidpb.IncrementRequestTelemetryResponse, error) {
	if s == nil || s.stream == nil {
		return nil, errors.New("apidgrpc: nil stream")
	}
	return s.stream.Recv()
}

// CloseSend half-closes the request stream.
func (s *requestTelemetryStream) CloseSend() error {
	if s == nil || s.stream == nil {
		return nil
	}
	return s.stream.CloseSend()
}
