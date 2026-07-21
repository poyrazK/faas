// Command vmmd — microVM supervisor: firecracker + jailer, the only root
// component (spec §4.4). vmmd owns everything that touches
// /usr/bin/firecracker and the jailer. It is the sole root-privileged daemon;
// per-VM work drops to the jailer immediately. Do not add a path that lets
// another component touch firecracker directly (spec §Component ownership).
//
// M1 wires the gRPC control surface (CreateFromSnapshot, CreateColdBoot,
// Pause+Snapshot, Destroy, Stats) per ADR-013..016. The control-plane TCP
// port is gated by the metrics_addr config field; the only required listen
// is the unix-domain socket at /run/faas/vmmd.sock.
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

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

const metricsPath = "/metrics"

func main() {
	wire.Daemon("vmmd", run)
}

// runDeps is the dependency-injection seam for testing. Production code
// uses the defaults; tests can swap individual fields to drive `run` without
// needing KVM, root, or a real /etc/faas/vmmd.toml.
type runDeps struct {
	configPath string                                                                                            // defaults to /etc/faas/vmmd.toml
	detectFC   func(context.Context) (string, error)                                                             // defaults to fcvm.DetectFirecrackerVersion
	listen     func(ctx context.Context, target string, tlsCfg *tls.Config, daemonUser string) (net.Listener, error) // defaults to wire.ListenAs (issue #95 / ADR-025)
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: envOrConfig("FAAS_VMMD_CONFIG", "/etc/faas/vmmd.toml"),
		detectFC:   fcvm.DetectFirecrackerVersion,
		listen:     wire.ListenAs,
	}
}

// envOrConfig returns the value of env key, or fallback when unset/empty.
// Named envOrConfig to avoid colliding with any same-named helper in
// cmd/<other-daemon> if these are ever linked into the same binary.
func envOrConfig(key, fallback string) string {
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
	log.Info("config", "listen_addr", listenTarget, "socket", cfg.SocketPath, "kernel", cfg.KernelPath,
		"metrics_addr", cfg.MetricsAddr)

	// Snapshots are pinned to the running Firecracker version (ADR-005);
	// detect it so restore only loads compatible snapshots and everything
	// else cold boots.
	fcVersion, err := deps.detectFC(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
	}

	cbm := fcvm.NewColdBootMetrics()
	mgr := fcvm.NewManager(
		wire.ExecRunner{},
		fcvm.NewJailerVMM(fcvm.JailChrootBase, 30*time.Second),
		fcvm.Paths{Kernel: cfg.KernelPath},
		fcVersion,
		log,
		cbm,
	)
	log.Info("vmmd ready", "fc_version", fcVersion, "max_slots", fcvm.MaxSlots,
		"uid_lo", fcvm.JailUIDBase, "uid_hi", fcvm.JailUIDMax)

	// Ops + listener. Resolve the listen target (issue #95): unix://
	// default, tcp/dns optional; tcp targets require a complete mTLS
	// cluster and the loader rejects partial configs.
	ops := wire.NewOpsMetrics("vmmd")
	serverTLS, err := cfg.LoadServerTLS()
	if err != nil {
		return fmt.Errorf("vmmd: load server TLS: %w", err)
	}
	lis, err := deps.listen(ctx, listenTarget, serverTLS, cfg.OwnerUser)
	if err != nil {
		return fmt.Errorf("vmmd: listen %s: %w", listenTarget, err)
	}
	gsrv := grpc.NewServer()
	impl := vmmdgrpc.New(mgr, ops, fcVersion, log)
	impl.Register(gsrv)

	// Optional /metrics endpoint.
	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle(metricsPath, ops.Handler())
		// Cold-boot fallback counter has its own registry (one writer,
		// one reader). Mount at /metrics/fallback so a scrape that only
		// wants the ops series stays clean.
		mux.Handle(metricsPath+"/fallback", cbm.Handler())
		httpSrv = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second, // match schedd; guards the metrics endpoint against Slowloris
		}
		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics http", "err", err)
			}
		}()
		log.Info("metrics listening", "addr", cfg.MetricsAddr)
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("grpc listening", "addr", listenTarget, "service", vmmdpb.Vmmd_ServiceDesc.ServiceName)
		serveErr <- gsrv.Serve(lis)
	}()

	// Heartbeat retains the §6.2 leak signal (live + leased must be 0 when idle).
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
heartbeat:
	for {
		select {
		case <-ctx.Done():
			log.Info("draining", "live", mgr.LiveCount())
			break heartbeat
		case <-tick.C:
			log.Debug("heartbeat", "live", mgr.LiveCount(), "leased", mgr.LeasedCount())
		case err := <-serveErr:
			if err != nil {
				return err
			}
		}
	}

	// Graceful shutdown — 5s deadline; M2 schedd may be holding a Connect
	// we don't want to drop before its replacement lease is acquired.
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gsrv.GracefulStop()
	if httpSrv != nil {
		//nolint:contextcheck // shutdown context must outlive caller ctx (which is already Done); detached from caller per gRPC + net/http contract.
		_ = httpSrv.Shutdown(stopCtx)
	}
	_ = lis.Close()
	return nil
}
