// Command realtimed owns Gregale managed realtime connections. The public
// gateway forwards the reserved /__gregale/realtime/ namespace to this daemon;
// endpoint and management operations are available only on its Unix socket.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
)

const defaultSocket = "/run/faas/realtimed.sock"
const defaultHealthListen = "127.0.0.1:9107"

func main() {
	wire.Daemon("realtimed", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	ops := wire.NewOpsMetrics("realtimed")
	traceShutdown, traceErr := trace.InitTracerWithRegistry(ctx, "realtimed", wire.Version, log, ops.Registry(), ops.MetricPrefix())
	if traceErr != nil {
		return fmt.Errorf("realtimed: init tracing: %w", traceErr)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("realtimed: trace shutdown failed", "err", err)
		}
	}()
	wire.BootStamps(ctx, "realtimed", ops)
	wire.RegisterDefaultOps(ops)

	observedRole := role.FromConfig(getenv("FAAS_REALTIME_ROLE", string(role.RoleSingleBox)), "FAAS_REALTIME_ROLE")
	if err := role.Require("realtimed", observedRole, role.RoleSingleBox, role.RoleComputeOnly); err != nil {
		return err
	}
	socketPath := getenv("FAAS_REALTIME_SOCKET", defaultSocket)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		return err
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	callbackTimeout := envDuration("FAAS_REALTIME_CALLBACK_TIMEOUT", 30*time.Second)
	outbox, err := realtime.NewCallbackOutbox(realtime.CallbackOutboxConfig{
		Root: getenv("FAAS_REALTIME_CALLBACK_OUTBOX", realtime.DefaultCallbackOutboxRoot),
	})
	if err != nil {
		return err
	}
	hooks := realtime.HTTPHooks{
		Client:       newCallbackHTTPClient(callbackTimeout),
		DurableQueue: outbox,
	}
	manager := realtime.NewManager(realtime.Config{
		MaxConnections:   envInt("FAAS_REALTIME_MAX_CONNECTIONS", 10_000),
		MaxMessageBytes:  int64(envInt("FAAS_REALTIME_MAX_MESSAGE_BYTES", 1<<20)),
		OutboundQueue:    envInt("FAAS_REALTIME_OUTBOUND_QUEUE", 64),
		Heartbeat:        envDuration("FAAS_REALTIME_HEARTBEAT", 30*time.Second),
		PongWait:         envDuration("FAAS_REALTIME_PONG_WAIT", 10*time.Second),
		WriteWait:        envDuration("FAAS_REALTIME_WRITE_WAIT", 5*time.Second),
		MaxConnectionAge: envDuration("FAAS_REALTIME_MAX_AGE", 24*time.Hour),
		CallbackTimeout:  callbackTimeout,
	}, hooks)
	defer func() { _ = manager.Close() }()
	ops.Registry().MustRegister(realtime.NewStatsCollector(manager))
	readyProbe := &wire.ReadyzProbe{}
	readySignal := readyProbe.Register()
	readyProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("realtimed", ready, reason)
	})
	defer readyProbe.Drain("realtimed", log)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if err := os.Chmod(socketPath, 0o660); err != nil {
		return err
	}
	healthListener, err := net.Listen("tcp", getenv("FAAS_REALTIME_HEALTH_LISTEN", defaultHealthListen))
	if err != nil {
		_ = listener.Close()
		return err
	}

	server := &http.Server{
		Handler:           trace.HTTPHandler("realtimed.internal", wire.HTTPMetricsHandler(ops, "http_request", manager.HTTPHandler())),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	controlMux := http.NewServeMux()
	controlMux.Handle("GET /metrics", ops.Handler())
	wire.ControlMuxLite(controlMux, readyProbe.ReadyFunc(), readyProbe.ReasonFunc())
	healthServer := &http.Server{
		// Keep the public health listener isolated from management routes. It
		// may expose only health/readiness and operator metrics.
		Handler:           trace.HTTPHandler("realtimed", wire.HTTPMetricsHandler(ops, "http_request", controlMux)),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		err := outbox.Run(ctx, func(deliveryCtx context.Context, event realtime.Event) error {
			callbackCtx, cancel := context.WithTimeout(deliveryCtx, callbackTimeout)
			defer cancel()
			return hooks.Deliver(callbackCtx, event)
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("realtimed callback outbox stopped", "err", err)
		}
	}()
	serverErr := make(chan error, 2)
	go func() {
		log.Info("realtimed listening", "socket", socketPath)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()
	readySignal.Set(true, "")
	go func() {
		log.Info("realtimed health listening", "address", healthListener.Addr().String())
		if err := healthServer.Serve(healthListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = manager.Close()
		_ = healthServer.Shutdown(shutdownCtx)
		return server.Shutdown(shutdownCtx)
	}
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// newCallbackHTTPClient is the production transport for customer-managed
// realtime callbacks. Callback URLs are customer-controlled, so the dialer
// must re-check the resolved address on every attempt; validating DNS only
// when an endpoint is created leaves a DNS-rebinding window at delivery time.
// Keep redirects disabled as a second boundary: a callback must not redirect
// its bearer credential or event payload to another host.
func newCallbackHTTPClient(timeout time.Duration) *http.Client {
	client := oci.NewEgressHTTPClient()
	// The loopback escape hatch is explicitly dev/test-only and defaults to
	// the guarded transport on production hosts.
	if testClient := oci.NewEgressHTTPClientAllowLoopback(); testClient != nil {
		client = testClient
	}
	client.Timeout = timeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}
