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

// startTraceReceiver registers the ADR-958 guest trace channel. It is on by
// default: whether an app may trace is decided by apid (the guest_tracing
// platform flag and the app's tracing.enabled), and the broker refuses frames
// from apps that have not enabled it. Gating the receiver on a node env var
// as well let the platform flag release tracing to customers while no node
// listened, so every export failed with "trace host unavailable".
// FAAS_GUEST_TRACING_ENABLED=0 turns it off on a node; =1 makes a setup
// failure fatal, as before. By default a setup failure is logged and vmmd
// starts without the receiver: tracing is never worth a node that will not
// boot.
func startTraceReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store state.Store, jailer *fcvm.JailerVMM) (func(), error) {
	required := false
	switch os.Getenv("FAAS_GUEST_TRACING_ENABLED") {
	case "0", "false":
		return func() {}, nil
	case "1":
		required = true
	}
	closeFn, err := registerTraceReceiver(ctx, log, mgr, store, jailer)
	if err != nil && !required {
		log.Warn("guest trace receiver not registered; in-guest tracing exports from this node will fail", "err", err)
		return func() {}, nil
	}
	return closeFn, err
}

func registerTraceReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store state.Store, jailer *fcvm.JailerVMM) (func(), error) {
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
