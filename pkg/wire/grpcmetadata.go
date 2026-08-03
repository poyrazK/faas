// Package wire — grpcmetadata.go is the gRPC-side counterpart to
// logging.go (issue #517). WithCorrelationOutgoing attaches a
// CorrelationFields struct to an outgoing gRPC context via the
// google.golang.org/grpc metadata package; CorrelationFromIncoming
// reads it back on the server side. Together they form the bridge
// that lets a single inbound request_id / wake_id cross from the
// HTTP edge (gatewayd) through gRPC (schedd → vmmd) and re-attach to
// every slog record the downstream daemon emits.
//
// Wire shape: the gRPC metadata keys mirror the HTTP header names
// (lowercase x-faas-…) but the suffix matches the slog field names
// (request_id, wake_id, app_id, deployment_id, instance_id,
// invocation_id). The dual naming keeps the gRPC layer compatible
// with the existing pkg/middleware/requestid.go header convention
// while the slog envelope stays log-canonical.
//
// Empty fields are skipped (not emitted as empty metadata entries) so
// the wire stays tight. A producer that knows only request_id + wake_id
// sends two keys, not six.
package wire

import (
	"context"

	"google.golang.org/grpc/metadata"
)

// gRPC metadata keys. Lowercase per the gRPC metadata convention —
// keys are case-insensitive on the wire and grpc-go normalises them
// to lowercase in the in-memory MD. The full header form (with x-faas-
// prefix) is used when reading HTTP headers; the gRPC keys drop the
// prefix because the prefix is meaningless inside the metadata carrier.
//
// x-faas-trace-id and x-faas-span-id are the OTel span-context carriers
// (issue #555). The runtime source of truth for OTel propagation is
// the W3C `traceparent` header (mounted by contrib/otelgrpc); these
// x-faas-* keys are an internal backup so the existing slog envelope
// can surface trace_id/span_id without re-parsing the traceparent
// header at every read site. Schedd stamps the x-faas-* keys onto the
// MD alongside the traceparent that otelgrpc writes.
const (
	mdKeyRequestID    = "x-faas-request-id"
	mdKeyWakeID       = "x-faas-wake-id"
	mdKeyAppID        = "x-faas-app-id"
	mdKeyDeploymentID = "x-faas-deployment-id"
	mdKeyInstanceID   = "x-faas-instance-id"
	mdKeyInvocationID = "x-faas-invocation-id"
	mdKeyTraceID      = "x-faas-trace-id"
	mdKeySpanID       = "x-faas-span-id"
)

// WithCorrelationOutgoing attaches fields to ctx as gRPC outgoing
// metadata. The returned ctx is suitable for passing to a gRPC client
// method; metadata.NewOutgoingContext is the canonical carrier.
//
// Nil ctx is treated as context.Background so callers can pass through
// whatever they have without a nil-check. Empty fields are skipped so
// the metadata set stays minimal — a producer that only knows the
// request_id sends one key, not six.
//
// Idempotent: calling WithCorrelationOutgoing twice on the same ctx
// appends a second MD layer (gRPC supports nested MD). The first read
// (in CorrelationFromIncoming) returns the most recent layer. This
// matters because schedd may add wake_id to a ctx that already carries
// request_id from gatewayd.
func WithCorrelationOutgoing(ctx context.Context, fields CorrelationFields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	pairs := make([]string, 0, 12)
	if fields.RequestID != "" {
		pairs = append(pairs, mdKeyRequestID, fields.RequestID)
	}
	if fields.WakeID != "" {
		pairs = append(pairs, mdKeyWakeID, fields.WakeID)
	}
	if fields.AppID != "" {
		pairs = append(pairs, mdKeyAppID, fields.AppID)
	}
	if fields.DeploymentID != "" {
		pairs = append(pairs, mdKeyDeploymentID, fields.DeploymentID)
	}
	if fields.InstanceID != "" {
		pairs = append(pairs, mdKeyInstanceID, fields.InstanceID)
	}
	if fields.InvocationID != "" {
		pairs = append(pairs, mdKeyInvocationID, fields.InvocationID)
	}
	if fields.TraceID != "" {
		pairs = append(pairs, mdKeyTraceID, fields.TraceID)
	}
	if fields.SpanID != "" {
		pairs = append(pairs, mdKeySpanID, fields.SpanID)
	}
	if len(pairs) == 0 {
		return ctx
	}
	md := metadata.Pairs(pairs...)
	prev, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		md = metadata.Join(prev, md)
	}
	return metadata.NewOutgoingContext(ctx, md)
}

// CorrelationFromIncoming reads the correlation fields from the inbound
// gRPC metadata on ctx. The boolean is true when at least one field was
// set (the gRPC MD was non-empty). The struct's zero value is returned
// otherwise — callers can pass through without a nil check.
//
// The reader walks every layer of incoming MD (gRPC nests MD across
// middleware; the first/last layer convention is documented in the
// grpc-go package's FromIncomingContext doc-comment). This is the
// correct behaviour for an envelope helper: schedd may have added
// wake_id to a ctx that already carries request_id from gatewayd, and
// the reader must surface both.
//
// Multi-valued MD entries (rare, only produced by proxies that duplicate
// a header) collide to the FIRST value via grpc-go's Get. Correlation
// IDs are opaque strings and the platform contract is single-valued,
// so this is consistent with the rest of the codebase; document here
// so any future caller that hits a duplicate-key case knows the
// behaviour is by design.
func CorrelationFromIncoming(ctx context.Context) (CorrelationFields, bool) {
	if ctx == nil {
		return CorrelationFields{}, false
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return CorrelationFields{}, false
	}
	var out CorrelationFields
	var any bool
	if v := md.Get(mdKeyRequestID); len(v) > 0 && v[0] != "" {
		out.RequestID = v[0]
		any = true
	}
	if v := md.Get(mdKeyWakeID); len(v) > 0 && v[0] != "" {
		out.WakeID = v[0]
		any = true
	}
	if v := md.Get(mdKeyAppID); len(v) > 0 && v[0] != "" {
		out.AppID = v[0]
		any = true
	}
	if v := md.Get(mdKeyDeploymentID); len(v) > 0 && v[0] != "" {
		out.DeploymentID = v[0]
		any = true
	}
	if v := md.Get(mdKeyInstanceID); len(v) > 0 && v[0] != "" {
		out.InstanceID = v[0]
		any = true
	}
	if v := md.Get(mdKeyInvocationID); len(v) > 0 && v[0] != "" {
		out.InvocationID = v[0]
		any = true
	}
	if v := md.Get(mdKeyTraceID); len(v) > 0 && v[0] != "" {
		out.TraceID = v[0]
		any = true
	}
	if v := md.Get(mdKeySpanID); len(v) > 0 && v[0] != "" {
		out.SpanID = v[0]
		any = true
	}
	return out, any
}
