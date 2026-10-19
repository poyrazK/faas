// Command outboundd is the explicit request-aware gateway for configured
// third-party integrations. Workloads opt in by calling /i/{integration_id}/;
// it is not a transparent packet proxy.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() { wire.Daemon("outboundd", run) }

func run(ctx context.Context, log *slog.Logger) error {
	ops := wire.NewOpsMetrics("outboundd")
	var retainedSpansAcc *gateway.SpansAccumulator
	var retainedSpansWriter *apidgrpc.SpansWriterClientImpl
	if os.Getenv("FAAS_OTEL_SPANS_WRITER_ENABLED") != "false" {
		retainedSpansAcc = gateway.NewSpansAccumulator()
		target := outboundSpansWriterTarget(os.Getenv)
		var dialErr error
		retainedSpansWriter, dialErr = apidgrpc.DialSpansWriter(ctx, target, nil)
		if dialErr != nil {
			return fmt.Errorf("outboundd: dial apid spans writer at %q: %w", target, dialErr)
		}
	}

	var traceShutdown func(context.Context) error
	var traceErr error
	if retainedSpansAcc != nil {
		traceShutdown, traceErr = trace.InitTracerWithRegistryAndExporters(
			ctx, "outboundd", wire.Version, log, ops.Registry(), ops.MetricPrefix(),
			gateway.NewRetainedServiceSpansExporter(retainedSpansAcc, log),
		)
	} else {
		traceShutdown, traceErr = trace.InitTracerWithRegistry(ctx, "outboundd", wire.Version, log, ops.Registry(), ops.MetricPrefix())
	}
	if traceErr != nil {
		if retainedSpansWriter != nil {
			_ = retainedSpansWriter.Close()
		}
		return fmt.Errorf("outboundd: init tracing: %w", traceErr)
	}
	var retainedFlushCancel context.CancelFunc
	var retainedFlushDone chan struct{}
	if retainedSpansAcc != nil && retainedSpansWriter != nil {
		flushInterval := 30 * time.Second
		if value := os.Getenv("FAAS_OTEL_FLUSH_INTERVAL"); value != "" {
			if parsed, parseErr := time.ParseDuration(value); parseErr == nil && parsed > 0 {
				flushInterval = parsed
			} else {
				log.Warn("outboundd: invalid OTel flush interval; using default", "value", value)
			}
		}
		flushCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		retainedFlushCancel = cancel
		retainedFlushDone = make(chan struct{})
		go func() {
			defer close(retainedFlushDone)
			if err := retainedSpansAcc.RunFlushLoop(flushCtx, gateway.FlushLoopConfig{
				Interval: flushInterval,
				WriteFn: func(writeCtx context.Context, traceID string, summaryJSON []byte, accountID string) (string, int64, error) {
					return retainedSpansWriter.WriteSpansSummary(writeCtx, traceID, summaryJSON, accountID)
				},
				Log: log,
				MaxSpansPerTrace: func(string) int {
					return api.MustLimitsFor(api.PlanScale).DebugTelemetrySpansPerTrace
				},
			}); err != nil {
				log.Error("outboundd: retained spans flush loop exited", "err", err)
			}
		}()
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("outboundd: trace shutdown failed", "err", err)
		}
		cancel()
		if retainedFlushCancel != nil {
			retainedFlushCancel()
			select {
			case <-retainedFlushDone:
			case <-time.After(6 * time.Second):
				log.Warn("outboundd: timed out draining retained spans")
			}
		}
		if retainedSpansWriter != nil {
			_ = retainedSpansWriter.Close()
		}
	}()
	wire.BootStamps(ctx, "outboundd", ops)
	wire.RegisterDefaultOps(ops)

	cfg, err := LoadConfig(defaultConfigPath())
	if err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, cfg.DBURL, "outboundd")
	if err != nil {
		return fmt.Errorf("outboundd: open db: %w", err)
	}
	defer pool.Close()
	// ADR-190 follow-up: export this pool's live statistics so the
	// DaemonMaxConnections cap above is measurable rather than arithmetic.
	wire.RegisterPoolMetrics(ops, pool)
	configured, err := cfg.Policies(os.Getenv)
	if err != nil {
		return err
	}
	if err := ValidateAIGateway(configured, cfg.AIGateway(os.Getenv)); err != nil {
		return err
	}
	identityVerifier, err := cfg.IdentityVerifier(configured)
	if err != nil {
		return fmt.Errorf("outboundd: workload identity: %w", err)
	}
	var executionIdentityVerifier outbound.ExecutionIdentityVerifier
	if identityVerifier != nil {
		var ok bool
		executionIdentityVerifier, ok = identityVerifier.(outbound.ExecutionIdentityVerifier)
		if !ok {
			return fmt.Errorf("outboundd: workload identity verifier does not support Runs")
		}
	}
	managedAuthorizations := make(map[string]string)
	needCustomerCredentials := false
	customerCredentialIDs := make([]string, 0)
	for _, item := range configured {
		if err := outbound.EnsureIntegration(ctx, pool, item.Record); err != nil {
			return fmt.Errorf("outboundd: provision integration %s: %w", item.Record.Policy.ID, err)
		}
		if item.providerAuthorization != "" {
			managedAuthorizations[item.Record.Policy.ID] = item.providerAuthorization
		}
		if item.Record.Policy.CredentialSource == outbound.CredentialSourceCustomerSealed {
			needCustomerCredentials = true
			customerCredentialIDs = append(customerCredentialIDs, item.Record.Policy.ID)
		}
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		return err
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		return err
	}
	executionAuthorizer, err := outbound.NewPostgresExecutionAuthorizer(pool)
	if err != nil {
		return err
	}
	handler, err := outbound.NewHandler(resolver, backend, nil)
	if err != nil {
		return err
	}
	if err := handler.SetManagedAuthorizations(managedAuthorizations); err != nil {
		return fmt.Errorf("outboundd: managed provider authorization: %w", err)
	}
	identityPath := os.Getenv("FAAS_FLEET_AGE_IDENTITY_PATH")
	if identityPath != "" {
		identity, err := secretbox.LoadHostKey(identityPath)
		if err != nil {
			return fmt.Errorf("outboundd: load fleet credential identity: %w", err)
		}
		credentialResolver, err := outbound.NewPostgresSealedCredentialResolver(pool, []*age.X25519Identity{identity}, customerCredentialIDs)
		if err != nil {
			return fmt.Errorf("outboundd: customer credential resolver: %w", err)
		}
		handler.CredentialResolver = credentialResolver
	} else if needCustomerCredentials {
		return errors.New("outboundd: customer-sealed credentials require FAAS_FLEET_AGE_IDENTITY_PATH")
	}
	handler.SimulationHandler = &outbound.SimulationHandler{Store: state.NewPgStore(pool)}
	handler.IdentityVerifier = identityVerifier
	handler.ExecutionIdentityVerifier = executionIdentityVerifier
	handler.ExecutionAuthorizer = executionAuthorizer
	if cfg.WorkflowOutboundEnabled || os.Getenv("FAAS_WORKFLOW_OUTBOUND_ENABLED") == "1" {
		authorizer, err := outbound.NewPostgresWorkflowAuthorizer(pool)
		if err != nil {
			return err
		}
		handler.WorkflowAuthorizer = authorizer
	}
	outboundMetrics, err := outbound.NewMetrics(ops.Registry())
	if err != nil {
		return fmt.Errorf("outboundd: register metrics: %w", err)
	}
	handler.Metrics = outboundMetrics
	handler.MaxBodyBytes = cfg.MaxBodyBytes
	handler.AIGatewayEnabled = cfg.AIGateway(os.Getenv)
	if handler.AIGatewayRollout, err = cfg.AIGatewayRollout(os.Getenv); err != nil {
		return fmt.Errorf("outboundd: %w", err)
	}
	log.Info("outbound AI gateway release gate", "enabled", handler.AIGatewayEnabled,
		"rollout_accounts", len(handler.AIGatewayRollout), "rollout_limited", handler.AIGatewayRollout != nil)
	// ADR-990: AI fallback routes are served on relay connections.
	if handler.Routes, err = outbound.NewPostgresRouteResolver(pool); err != nil {
		return err
	}
	go runAIUsagePrune(ctx, backend, log)
	// ADR-997: replays recorded by apid run through this daemon's handler.
	replayWorker := &outbound.AIReplayWorker{Store: backend, Handler: handler, Log: log}
	go replayWorker.Run(ctx)
	// ADR-1340: model canaries are judged here and rolled back when they do
	// worse than their route's first target.
	canaryEvaluator := &outbound.AIRouteCanaryEvaluator{Store: backend, Metrics: outboundMetrics, Log: log}
	go canaryEvaluator.Run(ctx)
	// ADR-1431: provider batches are settled here once they finish, and
	// evaluations replay captured calls against a candidate model.
	batchWorker := &outbound.AIBatchWorker{Backend: backend, Usage: backend, Handler: handler, Log: log}
	go batchWorker.Run(ctx)
	evaluationWorker := &outbound.AIEvaluationWorker{Store: backend, Handler: handler, Log: log}
	go evaluationWorker.Run(ctx)
	// ADR-20261019152416: enforced GitOps candidates cannot reach the gateway,
	// so their outbound bindings are probed here and receipted for activation.
	qualificationWorker := &outbound.QualificationProbeWorker{Store: state.NewPgStore(pool), Handler: handler, Log: log}
	go qualificationWorker.Run(ctx)
	relayHandler := outbound.WithSimulationRelay(trace.HTTPHandler("outboundd-relay", handler), handler.SimulationHandler)
	shutdownRelay, err := startRelay(ctx, cfg, pool, relayHandler, identityVerifier, outboundMetrics, log)
	if err != nil {
		return err
	}
	readyProbe := &wire.ReadyzProbe{}
	pgSignal, stopPGSignal := wire.NewPGPingSignal(ctx, pool, 5*time.Second)
	readyProbe.RegisterSignal(pgSignal, stopPGSignal)
	readyProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("outboundd", ready, reason)
	})
	defer readyProbe.Drain("outboundd", log)
	controlMux := http.NewServeMux()
	controlMux.Handle("GET /metrics", ops.Handler())
	wire.ControlMuxLite(controlMux, readyProbe.ReadyFunc(), readyProbe.ReasonFunc())
	controlMux.Handle("/", trace.HTTPHandler("outboundd", wire.HTTPMetricsHandler(ops, "http_request", handler)))
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      controlMux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	publicServer := startPublicServer(cfg, handler, backend, log)
	var metricsServer *http.Server
	if cfg.MetricsAddr != "" {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", ops.Handler())
		wire.ControlMuxLite(metricsMux, nil, nil)
		metricsServer = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           metricsMux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
		}
		go func() {
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("outboundd: metrics http", "err", err)
			}
		}()
		log.Info("outboundd metrics listening", "addr", cfg.MetricsAddr)
	}
	listener, err := wire.ListenInherited("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("outboundd: listen %s: %w", cfg.ListenAddr, err)
	}
	if err := wire.RequireInheritedListenersClaimed("outboundd"); err != nil {
		_ = listener.Close()
		return err
	}
	// All startup dependencies (config, Postgres, policy resolution, and the
	// metrics registry) are ready before the serving listener is entered. Keep
	// the daemon-level metric aligned with the /readyz contract; wire.Daemon
	// flips it back to 0 on shutdown or an early return.
	ops.MarkReady("outboundd", true, "")
	log.Info("outbound gateway listening", "addr", cfg.ListenAddr, "integrations", len(configured))
	var stops []func(context.Context)
	if metricsServer != nil {
		stops = append(stops, func(ctx context.Context) { _ = metricsServer.Shutdown(ctx) })
	}
	if publicServer != nil {
		stops = append(stops, func(ctx context.Context) { _ = publicServer.Shutdown(ctx) })
	}
	if shutdownRelay != nil {
		stops = append(stops, shutdownRelay)
	}
	return wire.ServeUntilShutdown(ctx, server, listener, outbounddDrainGrace, stops...)
}

// startPublicServer serves access-token callers on the public listener when
// one is configured (ADR-1431). It carries no control or metrics endpoints.
func startPublicServer(cfg *Config, handler *outbound.Handler, tokens outbound.AIAccessTokenStore, log *slog.Logger) *http.Server {
	if cfg.PublicListenAddr == "" {
		return nil
	}
	public := &outbound.PublicHandler{Handler: handler, Tokens: tokens}
	server := &http.Server{
		Addr:              cfg.PublicListenAddr,
		Handler:           trace.HTTPHandler("outboundd-public", public),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("outboundd: public http", "err", err)
		}
	}()
	log.Info("outbound AI gateway public listener", "addr", cfg.PublicListenAddr)
	return server
}

// outbounddDrainGrace bounds how long a stopping outboundd lets in-flight
// third-party and AI calls finish, inside systemd's default 90 s stop budget.
const outbounddDrainGrace = time.Duration(api.GatewayDrainGraceSeconds) * time.Second

// aiUsagePruner removes AI usage rollups past their retention (ADR-988).
type aiUsagePruner interface {
	PruneAIUsage(context.Context) (int64, error)
}

// runAIUsagePrune deletes expired AI usage rollups at startup and hourly.
// Deletion is idempotent, so concurrent replicas need no coordination.
func runAIUsagePrune(ctx context.Context, pruner aiUsagePruner, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		deleted, err := pruner.PruneAIUsage(pruneCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Warn("outboundd: prune AI usage rollups", "err", err)
		} else if deleted > 0 {
			log.Info("outboundd: pruned AI usage rollups", "rows", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func outboundSpansWriterTarget(getenv func(string) string) string {
	if getenv != nil {
		if target := getenv("FAAS_APID_OTEL_SPANS_WRITER_SOCKET"); target != "" {
			return target
		}
	}
	return "/run/faas/otel_spans_writer.sock"
}
