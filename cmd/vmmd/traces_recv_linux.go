//go:build linux

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// startTraceReceiver registers the ADR-829 guest trace channel when the
// installation enables it. v1 forwards to the single-box SpansWriter Unix
// socket; split-box compute nodes need a vmmd client identity for apid's
// private mTLS listener first (ADR-829 follow-up).
func startTraceReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store state.Store, jailer *fcvm.JailerVMM) (func(), error) {
	if os.Getenv("FAAS_GUEST_TRACING_ENABLED") != "1" {
		return func() {}, nil
	}
	if store == nil {
		return nil, fmt.Errorf("trace receiver requires the state store")
	}
	target := os.Getenv("FAAS_APID_OTEL_SPANS_WRITER_SOCKET")
	if target == "" {
		target = "/run/faas/otel_spans_writer.sock"
	}
	client, err := apidgrpc.DialGuestSpans(ctx, target, nil)
	if err != nil {
		return nil, err
	}
	broker := newTraceBroker(mgr, store, client)
	if err := jailer.RegisterGuestVsockStreamHandler(api.TraceVsockPort, func(instance string, conn net.Conn) (string, error) {
		return broker.handle(ctx, instance, conn)
	}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("register trace receiver port %d: %w", api.TraceVsockPort, err)
	}
	log.Info("guest trace receiver registered", "vsock_host_port", api.TraceVsockPort, "target", target)
	return func() { _ = client.Close() }, nil
}
