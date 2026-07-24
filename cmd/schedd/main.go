// Command schedd — scheduler and instance-lifecycle owner (spec §4.3).
//
// schedd is the ONLY writer to the instances table and the sole owner of the
// state machine (spec §Component ownership, §6). It runs admission control, the
// idle reaper, eviction, and cron in one process — single writer, no distributed
// locking. It serves a gRPC Wake/ReportActivity surface to gatewayd on
// /run/faas/schedd.sock (ADR-018) and dials vmmd on /run/faas/vmmd.sock to drive
// the microVM lifecycle (ADR-014).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

const metricsPath = "/metrics"

func main() {
	wire.Daemon("schedd", run)
}

// runDeps is the dependency-injection seam for testing. Production uses the
// defaults; tests swap fields to drive run without Postgres, KVM, or a socket.
type runDeps struct {
	configPath string
	openDB     func(context.Context, string) (*pgxpool.Pool, error)
	migrate    func(context.Context, *pgxpool.Pool) error
	detectFC   func(context.Context) (string, error)
	dialVMM    func(ctx context.Context, target string, tlsCfg *tls.Config) (sched.VMM, error)
	listen     func(ctx context.Context, target string, tlsCfg *tls.Config, owner string) (net.Listener, error)
	// subscribeDeletion is the producer-side seam for the
	// NotifyAccountDeletionPending consumer (ADR-026). nil = the
	// subscriber is not started (cmd/schedd's main wires the
	// production db.Subscribe adapter; tests inject a fake).
	subscribeDeletion func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
	// heartbeatInterval overrides sched.DefaultHeartbeatInterval for
	// tests that want a sub-second cadence. Zero falls back to the
	// production default (30s).
	heartbeatInterval time.Duration
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: envOr("FAAS_SCHEDD_CONFIG", "/etc/faas/schedd.toml"),
		openDB:     db.Open,
		migrate:    db.MigrateUp,
		detectFC:   fcvm.DetectFirecrackerVersion,
		dialVMM: func(ctx context.Context, target string, tlsCfg *tls.Config) (sched.VMM, error) {
			return sched.DialVMMContext(ctx, target, tlsCfg)
		},
		listen: wire.ListenAs,
		// Production wires db.Subscribe. Tests inject a fake channel
		// so the subscriber's Park path is exercised end-to-end
		// without standing up Postgres.
		subscribeDeletion: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyAccountDeletionPending})
		},
	}
}

// envOr returns the value of env key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	listenTarget := cfg.ResolveListenTarget()
	vmmTarget := cfg.ResolveVMMTarget()
	log.Info("config",
		"listen_addr", listenTarget,
		"vmmd_target", vmmTarget,
		"socket", cfg.SocketPath,
		"vmmd_socket", cfg.VMMDSocket,
		"metrics_addr", cfg.MetricsAddr)

	pool, err := deps.openDB(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := deps.migrate(ctx, pool); err != nil {
		return err
	}

	// Snapshots load only on the Firecracker version that made them (ADR-005);
	// detect it so the engine restores compatible snapshots and cold boots the
	// rest.
	fcVersion, err := deps.detectFC(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
	}

	// Issue #95 / ADR-025: dial vmmd through the location-transparent
	// helper. tcp/dns targets require the vmmd_tls_* cluster; nil TLS on
	// a unix target keeps single-box behaviour unchanged.
	vmmTLS, err := cfg.LoadVMMTLS()
	if err != nil {
		return fmt.Errorf("schedd: load vmmd TLS: %w", err)
	}
	store := state.NewPgStore(pool)

	// Issue #97 / ADR-025 axis 3: enumerate the active compute_nodes
	// once at startup and build a VMMRouter that dials vmmd per target
	// URL on demand. The legacy single-box fleet has exactly one row
	// (the synthetic 'default-local' seeded by migration 00024) so
	// the router degenerates to "dial that one vmmd.sock on first
	// RPC" — same behaviour as pre-#97, just behind a per-node lookup
	// that the Wake / Park / KillStuck flow now plumbs through.
	nodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		// Treat ctx-cancellation as a clean shutdown — the test
		// suite cancels during the bootstrap ActiveComputeNodes
		// call to verify a clean drain (TestRun_DrainsOnCancel,
		// PR #115 coverage gate). Returning the wrapped error
		// would surface a non-nil error on what is in fact a
		// graceful exit. Real I/O failures keep returning the
		// wrapped error unchanged.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("schedd: list active compute_nodes: %w", err)
	}
	nodeInfos := make([]sched.ComputeNodeInfo, 0, len(nodes))
	for _, n := range nodes {
		nodeInfos = append(nodeInfos, sched.ComputeNodeInfo{ID: n.ID, TargetURL: n.TargetURL})
	}
	vmmRouter := sched.NewVMMRouter(nodeInfos, deps.dialVMM, vmmTLS)

	ledger := sched.NewNodeLedger()
	ops := wire.NewOpsMetrics("schedd")
	// Dashboard gauges (spec §12): schedd owns the snapshots table and the
	// admission ledger, so the four fcvm_* gauges live here, not in vmmd.
	// The DashboardMetrics callbacks close over `store` (PG) and `ledger`
	// (in-memory resident accounting). The lv-fc percentage shells out to
	// `lvs`; on dev boxes where lvs is missing, the closure returns 0 and
	// the gauge degrades to "no data" (no error, no spike).
	dashGauges := fcvm.NewDashboardGauges(fcvm.DashboardMetrics{
		ListSnapshotStats: func(ctx context.Context) ([]fcvm.SnapshotStat, error) {
			rows, err := store.ListLiveSnapshotStats(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]fcvm.SnapshotStat, len(rows))
			for i, r := range rows {
				out[i] = fcvm.SnapshotStat{MemBytes: r.MemBytes, DiskBytes: r.DiskBytes}
			}
			return out, nil
		},
		ResidentBytes: func(_ context.Context) (int64, error) {
			return int64(ledger.ResidentRAM()) * 1024 * 1024, nil
		},
		LvFcUsedPct: fcvm.DefaultLvFcUsedPct(api.LvFcName),
	})
	engine, err := sched.NewEngine(ctx, store, ledger, vmmRouter, sched.PoolNotifier{Pool: pool}, fcVersion, log)
	if err != nil {
		// A bootstrap failure caused by a cancelled ctx is the
		// normal "test cancelled runWithDeps before startup
		// completed" path; not an error worth reporting. Anything
		// else (missing migration 00024, dropped Postgres
		// connection, etc.) is the loud failure F-2 added.
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("schedd: init engine: %w", err)
	}
	engine.WithOpsMetrics(ops)

	// Rebuild admission accounting from any instances still live from a prior
	// run before we start admitting new wakes.
	if err := engine.SeedLedger(ctx); err != nil {
		log.Warn("seed ledger", "err", err)
	}

	// gRPC surface for gatewayd (ADR-018): unix socket by default;
	// tcp requires the tls_* cluster and is issue #95.
	serverTLS, err := cfg.LoadServerTLS()
	if err != nil {
		return fmt.Errorf("schedd: load server TLS: %w", err)
	}
	lis, err := deps.listen(ctx, listenTarget, serverTLS, cfg.OwnerUser)
	if err != nil {
		return fmt.Errorf("schedd: listen %s: %w", listenTarget, err)
	}
	gsrv := grpc.NewServer()
	scheddgrpc.New(engine, ops, log).Register(gsrv)

	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle(metricsPath, ops.Handler())
		// Mount the §12 dashboard gauges on a sibling path so a
		// `curl /metrics` scrape returns the canonical schedd ops
		// series; Prometheus hits both paths.
		mux.Handle(metricsPath+"/fcvm", dashGauges.Handler())
		httpSrv = &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics http", "err", err)
			}
		}()
		log.Info("metrics listening", "addr", cfg.MetricsAddr)
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("grpc listening", "addr", listenTarget, "service", scheddpb.Schedd_ServiceDesc.ServiceName)
		serveErr <- gsrv.Serve(lis)
	}()

	log.Info("schedd ready",
		"ram_ceiling_mb", api.RAMAdmissionCeilingMB,
		"eviction_threshold_mb", sched.EvictionThresholdMB,
		"vcpu_slots", api.VCPUSlots,
		"fc_version", fcVersion)

	// ADR-026 deletion subscriber. Long-lived goroutine under the
	// same ctx as loop.Run. The subscriber is purely a drain (PR #83
	// review #6 collapsed the SubFn reconnect path); the production
	// schedule is "Subscribe once at startup, dial again on transient
	// errors". Linear 1s → 30s backoff lives here in cmd/schedd, not
	// inside pkg/sched, so the package stays a thin adapter. nil seam
	// = skip in tests that don't want a fake channel.
	if deps.subscribeDeletion != nil {
		sub := sched.NewDeletionSubscriber(engine, log)
		go func() {
			delay := 1 * time.Second
			const maxDelay = 30 * time.Second
			for {
				if ctx.Err() != nil {
					return
				}
				delFeed, delCancel, delErr := deps.subscribeDeletion(ctx, pool)
				if delErr != nil {
					log.Warn("schedd: deletion subscriber dial failed",
						"err", delErr, "retry_in", delay.String())
					select {
					case <-ctx.Done():
						return
					case <-time.After(delay):
					}
					if delay < maxDelay {
						delay *= 2
						if delay > maxDelay {
							delay = maxDelay
						}
					}
					continue
				}
				// Dial succeeded — run the drain on this channel
				// until it closes (a reconnect signal from
				// db.Subscribe) or ctx fires.
				err := sub.Run(ctx, delFeed)
				delCancel()
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Warn("schedd: deletion subscriber exited; retrying dial",
						"err", err, "retry_in", delay.String())
					select {
					case <-ctx.Done():
						return
					case <-time.After(delay):
					}
				}
				if ctx.Err() != nil {
					return
				}
				// Reset backoff after a successful drain that we
				// voluntarily tore down (rare in practice, but
				// keeps the curve sane after a partial outage).
				if err == nil || errors.Is(err, context.Canceled) {
					delay = 1 * time.Second
				}
			}
		}()
	}

	// PR #114 / ADR-025 axis 3: per-node liveness sweep. Every
	// `HeartbeatInterval` (default 30s) the heartbeat goroutine
	// dials a fresh *VMMClient per active node via deps.dialVMM
	// (issue #120), calls Ping, then Close — bypassing the
	// VMMRouter cache so every heartbeat pays the dial cost and
	// sees a fresh transport. deps.dialVMM already routes through
	// sched.DialVMMContext → pkg/overlay (issue #120), so the
	// heartbeat dial shares the same cross-box dial primitive as
	// the engine without an extra adapter. On success we stamp
	// last_heartbeat_at, on failure we flip active=false so
	// placement skips the dead node and the alertmanager rule
	// (PR #115) fires. Production cadence is overridable via
	// FAAS_HEARTBEAT_INTERVAL; tests inject a sub-second interval
	// through runDeps.heartbeatInterval to exercise the wiring.
	hb := sched.NewHeartbeat(store, sched.HeartbeatDialerFunc(deps.dialVMM), vmmTLS, log)
	hb.Interval = cfg.HeartbeatInterval
	hb.Staleness = cfg.HeartbeatStaleness
	if deps.heartbeatInterval > 0 {
		// Tests inject a sub-second cadence via runDeps to exercise
		// the wiring without waiting 30s for production cadence.
		hb.Interval = deps.heartbeatInterval
	}
	loop := sched.NewLoop(pool, engine, log).
		WithFlowCounter(flowcount.NewReader(wire.ExecRunner{})).
		WithWatchdog(sched.NewWatchdog(store, engine, log)).
		// PR #74: §17 retention sweep — DELETEs STOPPED/FAILED rows older
		// than cfg.RetentionDuration (defaults to api.DefaultInstanceRetention
		// when zero). Ticker fires at api.DefaultRetentionInterval (1h).
		WithRetention(sched.NewRetention(store, log).WithRetention(time.Duration(cfg.RetentionDuration))).
		WithHeartbeat(hb)
	// Cron dispatch path: route synthetic requests through gatewayd's
	// internal unix socket so metering + rate limits apply identically
	// to user traffic (spec §4.4, M7). A failure to dial is logged but
	// non-fatal — the cron loop tolerates a missing gateway (Wake still
	// runs, the synth step is best-effort).
	if cfg.GatewaySynthSocket != "" {
		synth, dialErr := sched.DialGatewaySynth(cfg.GatewaySynthSocket, log)
		if dialErr != nil {
			log.Warn("gateway synth dial: cron traffic will not flow until gatewayd is up",
				"socket", cfg.GatewaySynthSocket, "err", dialErr)
		} else {
			loop.WithGatewaySynth(synth)
		}
	}
	loopErr := make(chan error, 1)
	go func() { loopErr <- loop.Run(ctx) }()

	// Move 1 drain: a second goroutine inside schedd that drains the
	// unified invocations table on a 1s safety tick + invocation_due
	// pg_notify channel. Shares the engine + store with the cron
	// loop; the synth client is the same one the cron loop uses so
	// the wake path is one consistent admission gate.
	if cfg.GatewaySynthSocket != "" {
		synth, dialErr := sched.DialGatewaySynth(cfg.GatewaySynthSocket, log)
		if dialErr != nil {
			// A failed dial disables the entire drain — async /
			// queue / delayed-task rows would still arrive via the
			// 1s safety ticker (no notify) but every dispatch
			// would 502. Surface loud so the operator notices
			// before customers start timing out.
			log.Error("drain: synth dial failed; event-shaped dispatch is disabled",
				"socket", cfg.GatewaySynthSocket, "err", dialErr)
		} else {
			drain := sched.NewDrain(engine.Store(), engine,
				sched.WithDrainGatewaySynth(synth),
				sched.WithDrainNotifier(engine.Notifier()),
				sched.WithDrainLogger(log))
			notifC, subErr := db.SubscribeWithReconnect(ctx, pool,
				[]string{db.NotifyInvocationDue}, log)
			if subErr != nil {
				log.Error("drain: subscribe invocation_due failed; safety ticker still runs",
					"err", subErr)
			} else {
				go func() {
					if err := drain.Run(ctx, notifC); err != nil && !errors.Is(err, context.Canceled) {
						log.Warn("drain", "err", err)
					}
				}()
			}
		}
	}

	select {
	case <-ctx.Done():
		log.Info("draining")
	case err := <-serveErr:
		if err != nil {
			return err
		}
	case err := <-loopErr:
		if err != nil {
			return err
		}
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gsrv.GracefulStop()
	if httpSrv != nil {
		//nolint:contextcheck // shutdown context is intentionally detached from the already-cancelled caller ctx.
		_ = httpSrv.Shutdown(stopCtx)
	}
	_ = lis.Close()
	return nil
}
