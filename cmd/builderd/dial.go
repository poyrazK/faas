// Package main — dial.go wraps the production vmmd dial so
// cmd/builderd/readiness.go's defaultDial seam doesn't pull
// google.golang.org/grpc into every test (the readiness tests
// inject a stub via BuildReadinessProbe's dial param).
package main

import (
	"context"
	"crypto/tls"
	"github.com/onebox-faas/faas/pkg/wire"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// dialGRPC performs a non-blocking dial against target. We
// don't keep the connection open — the production driver
// (cmd/builderd/main.go) holds its own client. /readyz only
// needs to know "can we reach vmmd".
//
// The grpc v1 DialContext + WithBlock APIs are deprecated in
// grpc-go v1.63+; the replacement is grpc.NewClient but the
// v1.63+ API is async by design and doesn't preserve
// WithBlock's synchronous "did the handshake succeed within
// the deadline" semantics that the /readyz dial probe needs.
// Tracked for replacement once we lift all vmmd dials onto a
// shared pkg/vmmdgrpc.DialContext wrapper.
//
//nolint:staticcheck // SA1019: see doc above
func dialGRPC(ctx context.Context, target string) error {
	conn, err := grpc.DialContext(
		ctx,
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return err
	}
	// /readyz only needs the dial to succeed; close immediately.
	_ = conn.Close()
	return nil
}

func tlsReadinessDialer(tlsConfig *tls.Config) vmmdDialer {
	return func(ctx context.Context, target string) error {
		//nolint:staticcheck // WithBlock waits for the authenticated handshake before reporting ready.
		conn, err := wire.DialContext(ctx, target, tlsConfig, grpc.WithBlock())
		if err != nil {
			return err
		}
		return conn.Close()
	}
}
