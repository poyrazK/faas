// Package apidgrpc — spans_writer client (ADR-127 PR-D).
//
// gatewayd-public dials apid's SpansWriter service over the
// unix socket to flush its per-trace coalesce accumulator
// (Stage 3) into the request_telemetry.spans_summary jsonb
// column. The flush loop (Stage 4) drives the cadence — one
// RPC per accumulated trace_id every 30s.
//
// The client surface mirrors AuthClientImpl (Stage 2): same
// dial pattern, same pkg/wire.DialContext, same lazy-conn
// model. The shape difference: WriteSpansSummary returns a
// per-call outcome instead of a token/account tuple, so the
// gateway's flush loop can distinguish "inserted" (drop the
// entry), "rate_limited" (drop + retry next window), and
// "db_error" (keep + retry next window).

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

// SpansWriterClient is the gatewayd-public-side interface the
// flush loop holds. The interface is the union of every gRPC
// call gatewayd-public makes against apid's SpansWriter service.
type SpansWriterClient interface {
	// WriteSpansSummary ships one (trace_id, summary_json,
	// account_id) triple to apid's writer. The outcome ∈
	// {inserted, rate_limited, db_error} arrives on the
	// response; the gateway uses it to decide whether to
	// drop the accumulator entry (inserted / rate_limited) or
	// keep it for the next flush (db_error).
	WriteSpansSummary(ctx context.Context, traceID string, summaryJSON []byte, accountID string) (outcome string, retryAfterMs int64, err error)
	// WriteSummary ships a full request, including the app,
	// organization and customer-credential narrowing a producer
	// knows (gateway.SpanTarget.Request builds it).
	WriteSummary(ctx context.Context, req *apidpb.WriteSpansSummaryRequest) (outcome string, retryAfterMs int64, err error)
	Close() error
}

// SpansWriterClientImpl is the production implementation of
// SpansWriterClient. It owns the lazy gRPC connection to apid's
// unix socket.
type SpansWriterClientImpl struct {
	conn *grpc.ClientConn
	cli  apidpb.SpansWriterClient
}

// compile-time assertion that *SpansWriterClientImpl satisfies
// SpansWriterClient.
var _ SpansWriterClient = (*SpansWriterClientImpl)(nil)

// DialSpansWriter opens a lazy gRPC connection to apid's unix
// socket for the SpansWriter service. Same auth model as
// DialAuth: socket DAC mode 0660 group `faas` is the only auth
// in v1.0; transport uses insecure credentials over a trusted
// local socket. Connection dials on first RPC; Dial never
// blocks on apid being up.
func DialSpansWriter(ctx context.Context, target string, tlsCfg *tls.Config) (*SpansWriterClientImpl, error) {
	if target == "" {
		return nil, errors.New("apidgrpc: empty apid target for spans writer")
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("apidgrpc: dial apid %q for spans writer: %w", target, err)
	}
	return &SpansWriterClientImpl{conn: conn, cli: apidpb.NewSpansWriterClient(conn)}, nil
}

// NewSpansWriterClient wraps an already-dialed connection
// (used by bufconn tests in pkg/apidgrpc/).
func NewSpansWriterClient(conn *grpc.ClientConn) *SpansWriterClientImpl {
	return &SpansWriterClientImpl{conn: conn, cli: apidpb.NewSpansWriterClient(conn)}
}

// WriteSpansSummary ships one coalesced spans_summary payload
// to apid. On gRPC error (InvalidArgument, Unauthenticated,
// Internal, Unavailable) the gateway's flush loop drops the
// entry — the error envelope IS the outcome.
func (c *SpansWriterClientImpl) WriteSpansSummary(ctx context.Context, traceID string, summaryJSON []byte, accountID string) (string, int64, error) {
	return c.WriteSummary(ctx, &apidpb.WriteSpansSummaryRequest{
		TraceId:     traceID,
		SummaryJson: summaryJSON,
		AccountId:   accountID,
	})
}

// WriteSummary ships one coalesced summary with its full target.
func (c *SpansWriterClientImpl) WriteSummary(ctx context.Context, req *apidpb.WriteSpansSummaryRequest) (string, int64, error) {
	resp, err := c.cli.WriteSpansSummary(ctx, req)
	if err != nil {
		return "", 0, fmt.Errorf("apidgrpc: WriteSpansSummary: %w", err)
	}
	return resp.GetOutcome(), resp.GetRetryAfterMs(), nil
}

// Close shuts down the underlying gRPC connection. Safe to
// call multiple times; subsequent calls are no-ops.
func (c *SpansWriterClientImpl) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
