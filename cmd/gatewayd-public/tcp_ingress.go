package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpd"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

const defaultTCPDScheddTarget = "unix:///run/faas/schedd.sock"

// startTCPIngress is the production wiring for ADR-183. It remains opt-in;
// when enabled, tcpd binds only durable listener ports marked enabled and
// startup waits until the initial enabled-listener snapshot is bound.
func startTCPIngress(ctx context.Context, log *slog.Logger, store *state.PgStore, metrics *tcpmetrics.Metrics) (stop func(), drain func(context.Context) error, err error) {
	if !envBoolOr("FAAS_TCPD_ENABLED", false) {
		return func() {}, func(context.Context) error { return nil }, nil
	}
	if store == nil {
		return nil, nil, errors.New("gatewayd-public: tcpd requires a state store")
	}

	vmmdTLS, err := wire.LoadClientTLSConfigWithPrefix(
		"tcpd_vmmd_",
		os.Getenv("FAAS_TCPD_VMMD_TLS_CERT_PATH"),
		os.Getenv("FAAS_TCPD_VMMD_TLS_KEY_PATH"),
		os.Getenv("FAAS_TCPD_VMMD_TLS_CA_PATH"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("gatewayd-public: load tcpd vmmd TLS: %w", err)
	}
	scheddTLS, err := wire.LoadClientTLSConfigWithPrefix(
		"tcpd_schedd_",
		os.Getenv("FAAS_TCPD_SCHEDD_TLS_CERT_PATH"),
		os.Getenv("FAAS_TCPD_SCHEDD_TLS_KEY_PATH"),
		os.Getenv("FAAS_TCPD_SCHEDD_TLS_CA_PATH"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("gatewayd-public: load tcpd schedd TLS: %w", err)
	}
	sched, err := scheddgrpc.DialContext(ctx, envOr("FAAS_TCPD_SCHEDD_TARGET", defaultTCPDScheddTarget), scheddTLS)
	if err != nil {
		return nil, nil, fmt.Errorf("gatewayd-public: dial tcpd schedd: %w", err)
	}

	// Reuse the same placement identity and vmmd transport as the HTTP gateway.
	// The cache is local to this process and closes only after tcpd has drained.
	gateway.SetNodeResolver(func(resolveCtx context.Context, nodeID string) (string, bool) {
		node, resolveErr := store.ComputeNodeByID(resolveCtx, nodeID)
		if resolveErr != nil {
			if !errors.Is(resolveErr, state.ErrNotFound) {
				log.Warn("tcpd: resolve compute node", "node_id", nodeID, "err", resolveErr)
			}
			return "", false
		}
		if !node.Active && node.Lifecycle != state.NodeLifecycleDraining {
			return "", false
		}
		return node.TargetURL, node.TargetURL != ""
	})
	nodes := gateway.NewNodeClientCache(func(dialCtx context.Context, target string) (*grpc.ClientConn, error) {
		return overlay.Dial(dialCtx, overlay.New(target), vmmdTLS)
	}, log)
	closeDependencies := func() {
		_ = sched.Close()
		_ = nodes.Close()
	}

	maxBytes := api.RawTCPStreamMaxBytes
	if raw := os.Getenv("FAAS_TCPD_MAX_BYTES"); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || parsed <= 0 {
			closeDependencies()
			return nil, nil, fmt.Errorf("gatewayd-public: FAAS_TCPD_MAX_BYTES must be a positive integer, got %q", raw)
		}
		maxBytes = parsed
	}
	maxConnections := 0
	if raw := os.Getenv("FAAS_TCPD_MAX_CONNECTIONS"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			closeDependencies()
			return nil, nil, fmt.Errorf("gatewayd-public: FAAS_TCPD_MAX_CONNECTIONS must be a non-negative integer, got %q", raw)
		}
		maxConnections = parsed
	}
	maxConnectionsPerAccount := 0
	if raw := os.Getenv("FAAS_TCPD_MAX_CONNECTIONS_PER_ACCOUNT"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			closeDependencies()
			return nil, nil, fmt.Errorf("gatewayd-public: FAAS_TCPD_MAX_CONNECTIONS_PER_ACCOUNT must be a non-negative integer, got %q", raw)
		}
		maxConnectionsPerAccount = parsed
	}
	idleTimeout := api.StreamingIdleTimeoutDefault
	if raw := os.Getenv("FAAS_TCPD_IDLE_TIMEOUT"); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 {
			closeDependencies()
			return nil, nil, fmt.Errorf("gatewayd-public: FAAS_TCPD_IDLE_TIMEOUT must be a positive duration, got %q", raw)
		}
		idleTimeout = parsed
	}
	refreshInterval := 2 * time.Second
	if raw := os.Getenv("FAAS_TCPD_REFRESH_INTERVAL"); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 {
			closeDependencies()
			return nil, nil, fmt.Errorf("gatewayd-public: FAAS_TCPD_REFRESH_INTERVAL must be a positive duration, got %q", raw)
		}
		refreshInterval = parsed
	}

	serveReady := make(chan struct{})
	serveResult := make(chan error, 1)
	supervisor := &tcpd.Supervisor{
		BindHost:                 envOr("FAAS_TCPD_BIND_HOST", "0.0.0.0"),
		Source:                   store,
		Routes:                   tcpd.ListenerStoreResolver{Store: store},
		Targets:                  &tcpd.StoreTargetResolver{Instances: store, Admitter: sched},
		Forwarder:                gateway.TCPForwarder{Nodes: nodes, MaxBytes: maxBytes, IdleTimeout: idleTimeout, Metrics: metrics},
		RefreshInterval:          refreshInterval,
		MaxConnections:           maxConnections,
		MaxConnectionsPerAccount: maxConnectionsPerAccount,
		Metrics:                  metrics,
		OnReady:                  func() { close(serveReady) },
		OnError: func(err error) {
			log.Error("gatewayd-public: tcpd runtime error", "err", err)
		},
	}
	// Keep the raw listener context independent of wire.Daemon's first
	// signal. runDrain closes accepts and waits for active sessions under the
	// shared grace budget; a second signal or timeout cancels this context.
	tcpCtx, tcpCancel := context.WithCancel(context.WithoutCancel(ctx))
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		serveErr := supervisor.Serve(tcpCtx)
		serveResult <- serveErr
		if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			log.Error("gatewayd-public: tcpd stopped", "err", serveErr)
		}
	}()
	select {
	case <-serveReady:
	case serveErr := <-serveResult:
		tcpCancel()
		<-serveDone
		closeDependencies()
		if serveErr == nil {
			serveErr = errors.New("tcpd stopped before initial listeners were ready")
		}
		return nil, nil, fmt.Errorf("gatewayd-public: start tcpd: %w", serveErr)
	case <-ctx.Done():
		tcpCancel()
		<-serveDone
		closeDependencies()
		return nil, nil, ctx.Err()
	}
	log.Info("gatewayd-public: raw TCP ingress enabled", "bind_host", supervisor.BindHost, "refresh_interval", refreshInterval)

	var stopOnce sync.Once
	stop = func() {
		stopOnce.Do(func() {
			tcpCancel()
			<-serveDone
			closeDependencies()
		})
	}
	return stop, func(drainCtx context.Context) error {
		err := supervisor.Drain(drainCtx)
		stop()
		return err
	}, nil
}
