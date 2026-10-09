//go:build linux

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// startTraceReceiver registers the ADR-829 guest trace channel when the
// installation enables it.
func startTraceReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store state.Store, jailer *fcvm.JailerVMM) (func(), error) {
	if os.Getenv("FAAS_GUEST_TRACING_ENABLED") != "1" {
		return func() {}, nil
	}
	if store == nil {
		return nil, fmt.Errorf("trace receiver requires the state store")
	}
	target, tlsCfg, err := traceSpansWriterTarget(os.Getenv)
	if err != nil {
		return nil, err
	}
	client, err := apidgrpc.DialGuestSpans(ctx, target, tlsCfg)
	if err != nil {
		return nil, err
	}
	broker := newTraceBroker(mgr, store, client)
	logProblem := newTraceProblemLogger(log, time.Now)
	if err := jailer.RegisterGuestVsockStreamHandler(api.TraceVsockPort, func(instance string, conn net.Conn) (string, error) {
		reason, err := broker.handle(ctx, instance, conn)
		logProblem(instance, reason, err)
		return reason, err
	}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("register trace receiver port %d: %w", api.TraceVsockPort, err)
	}
	log.Info("guest trace receiver registered", "vsock_host_port", api.TraceVsockPort, "target", target, "mtls", tlsCfg != nil)
	return func() { _ = client.Close() }, nil
}
