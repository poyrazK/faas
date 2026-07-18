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
	"errors"
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
	dialVMM    func(socket string) (sched.VMM, error)
	listen     func(path, owner string) (net.Listener, error)
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: envOr("FAAS_SCHEDD_CONFIG", "/etc/faas/schedd.toml"),
		openDB:     db.Open,
		migrate:    db.MigrateUp,
		detectFC:   fcvm.DetectFirecrackerVersion,
		dialVMM:    func(socket string) (sched.VMM, error) { return sched.DialVMM(socket) },
		listen:     wire.ListenOrRecreateByName,
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

	vmm, err := deps.dialVMM(cfg.VMMDSocket)
	if err != nil {
		return err
	}

	store := state.NewPgStore(pool)
	ledger := sched.NewLedger()
	ops := wire.NewOpsMetrics("schedd")
	engine := sched.NewEngine(store, ledger, vmm, sched.PoolNotifier{Pool: pool}, fcVersion, log)

	// Rebuild admission accounting from any instances still live from a prior
	// run before we start admitting new wakes.
	if err := engine.SeedLedger(ctx); err != nil {
		log.Warn("seed ledger", "err", err)
	}

	// gRPC surface for gatewayd (ADR-018): unix socket, mode 0660 group `faas`.
	lis, err := deps.listen(cfg.SocketPath, cfg.OwnerUser)
	if err != nil {
		return err
	}
	gsrv := grpc.NewServer()
	scheddgrpc.New(engine, ops, log).Register(gsrv)

	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle(metricsPath, ops.Handler())
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
		log.Info("grpc listening", "socket", cfg.SocketPath, "service", scheddpb.Schedd_ServiceDesc.ServiceName)
		serveErr <- gsrv.Serve(lis)
	}()

	log.Info("schedd ready",
		"ram_ceiling_mb", api.RAMAdmissionCeilingMB,
		"eviction_threshold_mb", sched.EvictionThresholdMB,
		"vcpu_slots", api.VCPUSlots,
		"fc_version", fcVersion)

	loop := sched.NewLoop(pool, engine, log).
		WithFlowCounter(flowcount.NewReader(wire.ExecRunner{}))
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
