// Package wire — otelgrpc.go holds the canonical gRPC stats
// handler wiring for issue #555 PR-3. Uses otelgrpc's stats handlers
// (the new v0.69 API uses grpc.StatsHandler instead of the older
// grpc.UnaryServerInterceptor / grpc.StreamServerInterceptor
// pattern), so the per-daemon boot path can mount trace propagation
// without depending on the contrib package directly.
//
// Two helpers:
//
//   - TraceServerHandlers: returns []grpc.ServerOption with the
//     server-side stats handler that extracts W3C traceparent from
//     incoming metadata and starts / ends spans per RPC. Append to
//     the existing wire.ServerCredsOrEmpty(...) list before
//     grpc.NewServer.
//
//   - TraceDialHandlers: returns []grpc.DialOption with the
//     client-side stats handler. Append to the existing dial.
//
// Why wrap rather than re-export: pkg/wire is the layer every
// daemon depends on; pinning the contrib import here keeps the
// daemon's gRPC wiring trivial. The handler impl is otelgrpc's;
// we just freeze the version pin and the option list.
package wire

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// TraceServerOptions returns the server-side otelgrpc options. The
// caller mounts the resulting handler via grpc.StatsHandler on
// grpc.NewServer:
//
//	gsrv := grpc.NewServer(
//	    append(
//	        wire.ServerCredsOrEmpty(tlsCfg),
//	        wire.TraceServerOptions()...,
//	    )...,
//	)
//
// Always returns a non-empty slice (the handler is unconditional;
// the SDK's noop fallback in PR-1 means the traceparent round-trip
// is non-functional when no exporter is configured, but the
// handler still won't error).
func TraceServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
}

// TraceDialOptions returns the client-side otelgrpc options. Mount
// via grpc.WithStatsHandler on the dial:
//
//	conn, err := grpc.DialContext(
//	    ctx, target,
//	    wire.TraceDialOptions()...,
//	)
//
// The caller can compose with other dial options (TLS, retry,
// deadline) by appending to the opts slice.
func TraceDialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
}
