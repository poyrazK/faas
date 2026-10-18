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
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/genslot"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

const defaultSocket = "/run/faas/realtimed.sock"
const defaultHealthListen = "127.0.0.1:9107"

func main() {
	wire.HandoffDaemon("realtimed", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	// stopping is the unit's SIGTERM. ctx also ends once a two-generation
	// hand-off's successor serves (ADR-1580).
	stopping := ctx
	ctx, endGeneration := context.WithCancel(ctx)
	defer endGeneration()
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

	callbackTimeout := envDuration("FAAS_REALTIME_CALLBACK_TIMEOUT", 30*time.Second)
	outboxRoot := getenv("FAAS_REALTIME_CALLBACK_OUTBOX", realtime.DefaultCallbackOutboxRoot)
	if outboxRoot == realtime.DefaultCallbackOutboxRoot {
		if err := realtime.MigrateCallbackOutbox(realtime.LegacyCallbackOutboxRoot, outboxRoot); err != nil {
			return fmt.Errorf("realtimed: migrate callback outbox: %w", err)
		}
	}
	// Each generation owns a slot of the outbox, so a hand-off's two
	// generations never deliver the same callback file (ADR-1580).
	outboxSlot, err := genslot.Acquire(outboxRoot)
	if err != nil {
		return fmt.Errorf("realtimed: claim callback outbox: %w", err)
	}
	handedOff := false
	defer func() { returnCallbackOutboxHome(outboxSlot, handedOff, log) }()
	outbox, err := realtime.NewCallbackOutbox(realtime.CallbackOutboxConfig{
		Root:               outboxSlot.Dir(),
		DeadLetterMaxBytes: int64(envInt("FAAS_REALTIME_CALLBACK_DEAD_MAX_BYTES", int(realtime.DefaultCallbackDeadLetterMaxBytes))),
		ReplayWorkers:      envInt("FAAS_REALTIME_CALLBACK_REPLAY_WORKERS", realtime.DefaultCallbackOutboxReplayWorkers),
		MaxRetryInterval:   envDuration("FAAS_REALTIME_CALLBACK_RETRY_MAX_INTERVAL", realtime.DefaultCallbackOutboxMaxRetryInterval),
	})
	if err != nil {
		return err
	}
	adoptCallbackOutboxes(outboxSlot, outbox, log)
	go func() {
		t := time.NewTicker(genslot.AdoptInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				adoptCallbackOutboxes(outboxSlot, outbox, log)
			}
		}
	}()
	hooks := realtime.HTTPHooks{
		Client:       newCallbackHTTPClient(callbackTimeout),
		DurableQueue: outbox,
	}
	resumePreview := os.Getenv("FAAS_REALTIME_RESUME_PREVIEW_ENABLED") == "1"
	ticketPreview := os.Getenv("FAAS_REALTIME_TICKETS_PREVIEW_ENABLED") == "1"
	accessPreview := os.Getenv("FAAS_REALTIME_ACCESS_PREVIEW_ENABLED") == "1"
	// ADR-1323: record per-endpoint usage with apid. Recorded only; nothing
	// bills or limits from it yet.
	usageEnabled := os.Getenv("FAAS_REALTIME_USAGE_ENABLED") == "1"
	// ADR-1323 fair use and node outbound budget, and ADR-1324 occupancy, stay
	// off until realtime has been tested end to end.
	fairUse := os.Getenv("FAAS_REALTIME_FAIR_USE_ENABLED") == "1"
	outboundBudget := os.Getenv("FAAS_REALTIME_OUTBOUND_BUDGET_ENABLED") == "1"
	occupancyPreview := os.Getenv("FAAS_REALTIME_OCCUPANCY_PREVIEW_ENABLED") == "1"
	var outboundNodeBytes int64
	if outboundBudget {
		outboundNodeBytes = int64(envInt("FAAS_REALTIME_OUTBOUND_NODE_BYTES", api.RealtimeNodeOutboundBytesDefault))
		// The budget is sized against this soft limit; an explicit
		// GOMEMLIMIT wins.
		if strings.TrimSpace(os.Getenv("GOMEMLIMIT")) == "" {
			debug.SetMemoryLimit(api.RealtimeMemoryLimitBytes)
		}
	}
	var ticketReader realtime.SubscriptionTicketReader
	var accessReader realtime.ManagedRealtimeAccessReader
	var historyReader realtime.ManagedRealtimeHistoryReader
	var routeReporter realtime.ManagedRealtimeChannelRouteReporter
	var fleetClient realtime.ManagedRealtimeFleetClient
	var usageReporter realtime.UsageReporter
	var connectionSync realtime.ConnectionSyncer
	var occupancyClient realtime.ManagedRealtimeOccupancyClient
	if resumePreview || accessPreview || ticketPreview || usageEnabled || fairUse || occupancyPreview {
		historyTLS, tlsErr := wire.LoadClientTLSConfigWithPrefix("realtime_history_",
			os.Getenv("FAAS_REALTIME_HISTORY_TLS_CERT_PATH"),
			os.Getenv("FAAS_REALTIME_HISTORY_TLS_KEY_PATH"),
			os.Getenv("FAAS_REALTIME_HISTORY_TLS_CA_PATH"))
		if tlsErr != nil {
			return fmt.Errorf("realtimed: history TLS: %w", tlsErr)
		}
		nodeName := strings.TrimSpace(os.Getenv("FAAS_NODE_NAME"))
		if nodeName == "" {
			nodeName = state.DefaultLocalNodeName
		}
		reader, dialErr := apidgrpc.DialRealtimeHistory(ctx,
			getenv("FAAS_REALTIME_HISTORY_TARGET", "/run/faas/request_telemetry.sock"), historyTLS, nodeName)
		if dialErr != nil {
			return fmt.Errorf("realtimed: history reader: %w", dialErr)
		}
		defer func() { _ = reader.Close() }()
		historyReader = reader
		accessReader = reader
		ticketReader = reader
		routeReporter = reader
		fleetClient = reader
		if usageEnabled {
			usageReporter = reader
		}
		// Counts feed fair use and the customer usage API's open connections.
		if fairUse || usageEnabled {
			connectionSync = reader
		}
		// A single box has one realtime node, whose own counts are exact
		// (ADR-1324); only split nodes ask apid for the others.
		if occupancyPreview && observedRole == role.RoleComputeOnly {
			occupancyClient = reader
		}
	}
	manager := realtime.NewManager(realtime.Config{
		// Zero selects the package default, which depends on FairUse.
		MaxConnections:       envInt("FAAS_REALTIME_MAX_CONNECTIONS", 0),
		MaxMessageBytes:      int64(envInt("FAAS_REALTIME_MAX_MESSAGE_BYTES", 1<<20)),
		OutboundQueue:        envInt("FAAS_REALTIME_OUTBOUND_QUEUE", 64),
		OutboundQueueBytes:   int64(envInt("FAAS_REALTIME_OUTBOUND_QUEUE_BYTES", 4<<20)),
		OutboundNodeBytes:    outboundNodeBytes,
		Heartbeat:            envDuration("FAAS_REALTIME_HEARTBEAT", 30*time.Second),
		PongWait:             envDuration("FAAS_REALTIME_PONG_WAIT", 10*time.Second),
		WriteWait:            envDuration("FAAS_REALTIME_WRITE_WAIT", 5*time.Second),
		MaxConnectionAge:     envDuration("FAAS_REALTIME_MAX_AGE", 24*time.Hour),
		CallbackTimeout:      callbackTimeout,
		JWTAuthorizer:        newRealtimeJWTAuthorizer(log),
		ResumePreview:        resumePreview,
		TicketPreview:        ticketPreview,
		TicketReader:         ticketReader,
		AccessPreview:        accessPreview,
		AccessReader:         accessReader,
		HistoryReader:        historyReader,
		ChannelRouteReporter: routeReporter,
		OccupancyPreview:     occupancyPreview,
		OccupancyClient:      occupancyClient,
		FleetClient:          fleetClient,
		FleetReportFailure: func(err error) {
			log.Warn("realtimed: fleet presence or ephemeral relay failed", "err", err)
		},
		ChannelRouteReportFailure: func(err error) {
			log.Warn("realtimed: channel route report failed; apid reconciliation will repair it", "err", err)
		},
		UsageReporter: usageReporter,
		FairUse:       fairUse,
		// Plan connection limits hold fleet-wide whenever apid is reachable;
		// without it they hold per node (ADR-1323).
		ConnectionSync: connectionSync,
		ConnectionSyncFailure: func(err error) {
			log.Warn("realtimed: connection count sync failed; account limits use local counts until it recovers", "err", err)
		},
		UsageReportFailure: func(err error) {
			log.Warn("realtimed: usage report failed; the hour is kept and retried", "err", err)
		},
	}, hooks)
	defer func() { _ = manager.Close() }()
	callbackReplayRestarts := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "realtimed_callback_replay_supervisor_restarts_total",
		Help: "Callback replay loop restarts after unexpected exits since process start.",
	})
	ops.Registry().MustRegister(realtime.NewStatsCollector(manager), callbackReplayRestarts)
	readyProbe := &wire.ReadyzProbe{}
	readySignal := readyProbe.Register()
	readyProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("realtimed", ready, reason)
	})
	defer readyProbe.Drain("realtimed", log)

	listener, err := listenRealtimeSocket(socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	healthListener, err := wire.ListenInherited("tcp", getenv("FAAS_REALTIME_HEALTH_LISTEN", defaultHealthListen))
	if err != nil {
		_ = listener.Close()
		return err
	}
	if err := wire.RequireInheritedListenersClaimed("realtimed"); err != nil {
		_ = listener.Close()
		_ = healthListener.Close()
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
	replayDone := make(chan struct{})
	// The outbox returns home (deferred above) only after replay stops.
	defer func() { endGeneration(); <-replayDone }()
	go func() {
		defer close(replayDone)
		superviseCallbackReplay(ctx, log, callbackReplayRetryInitial, callbackReplayRetryMax, func(replayCtx context.Context) error {
			return outbox.Run(replayCtx, func(deliveryCtx context.Context, event realtime.Event) error {
				callbackCtx, cancel := context.WithTimeout(deliveryCtx, callbackTimeout)
				defer cancel()
				return hooks.Deliver(callbackCtx, event)
			})
		}, callbackReplayRestarts.Inc)
	}()
	// The drains let a connection accepted just before shutdown deliver its
	// request; net/http drops one it reads after Shutdown began (ADR-1580).
	// During a hand-off the successor accepts on the same sockets.
	serverDrain, healthDrain := wire.NewHTTPDrain(server), wire.NewHTTPDrain(healthServer)
	serverErr := make(chan error, 2)
	go func() {
		log.Info("realtimed listening", "socket", socketPath)
		if err := serverDrain.ServeErr(server.Serve(serverDrain.Listener(listener))); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()
	readySignal.Set(true, "")
	go func() {
		log.Info("realtimed health listening", "address", healthListener.Addr().String())
		if err := healthDrain.ServeErr(healthServer.Serve(healthDrain.Listener(healthListener))); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	notifyStop := wire.NotifyReadyWhen(ctx, readyProbe.ReadyFunc())
	defer notifyStop()
	defer wire.StartWatchdog(ctx, wire.NewLiveness(), ops, log)()
	// WebSocket fan-out is per process: a session left on the old generation
	// would miss what is published to the successor. A hand-off therefore
	// closes the old generation's sessions once the successor serves, and
	// their clients reconnect to it at once.
	handoff := make(chan struct{})
	wire.WatchHandoffs(ctx, "realtimed", log, func() {
		close(handoff)
		endGeneration()
	})
	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		select {
		case <-handoff:
			handedOff = true
		default:
		}
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(stopping), 10*time.Second)
		defer cancel()
		_ = manager.Close()
		healthDone := make(chan struct{})
		go func() { defer close(healthDone); _ = healthDrain.Shutdown(shutdownCtx, healthServer) }()
		err := serverDrain.Shutdown(shutdownCtx, server)
		<-healthDone
		return err
	}
}

// listenRealtimeSocket prefers the socket faas-realtimed.socket keeps open
// across restarts: gatewayd-internal's realtime connects and management
// calls then wait for the replacement instead of failing.
func listenRealtimeSocket(path string) (net.Listener, error) {
	if l, ok, err := wire.InheritedListener("unix", path); err != nil {
		return nil, err
	} else if ok {
		return l, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = l.Close()
		return nil, err
	}
	wire.Share(l) // a hand-off successor serves the same socket (ADR-1580)
	return l, nil
}

// adoptCallbackOutboxes moves callbacks an exited generation left in its
// outbox slot into this generation's outbox.
func adoptCallbackOutboxes(slot *genslot.Slot, outbox *realtime.CallbackOutbox, log *slog.Logger) {
	err := slot.AdoptOrphans(func(dir string) error {
		n, err := outbox.Absorb(dir)
		if n > 0 {
			log.Info("realtimed: adopted an exited generation's callbacks", "callbacks", n, "dir", dir)
		}
		return err
	})
	if err != nil {
		log.Warn("realtimed: adopt callbacks; retrying", "err", err)
	}
}

// returnCallbackOutboxHome moves this generation's callbacks to the root
// slot at exit, where a release without slots looks. A handed-off generation
// does not wait: its successor adopts them.
func returnCallbackOutboxHome(slot *genslot.Slot, handedOff bool, log *slog.Logger) {
	wait := 5 * time.Second
	if handedOff {
		wait = 0
	}
	err := slot.ReturnHome(wait, func(from, home string) error {
		q, err := realtime.NewCallbackOutbox(realtime.CallbackOutboxConfig{Root: home})
		if err != nil {
			return err
		}
		_, err = q.Absorb(from)
		return err
	})
	if err != nil {
		log.Warn("realtimed: callbacks stay for the next generation", "err", err)
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
