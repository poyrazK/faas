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
	"errors"
	"log/slog"
	"net"
	"net/http"
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
	configPath string                                     // defaults to /etc/faas/vmmd.toml
	detectFC   func(context.Context) (string, error)      // defaults to fcvm.DetectFirecrackerVersion
	listen     func(string, string) (net.Listener, error) // defaults to wire.ListenOrRecreateByName
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: "/etc/faas/vmmd.toml",
		detectFC:   fcvm.DetectFirecrackerVersion,
		listen:     wire.ListenOrRecreateByName,
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	log.Info("config", "socket", cfg.SocketPath, "kernel", cfg.KernelPath,
		"metrics_addr", cfg.MetricsAddr)

	// Snapshots are pinned to the running Firecracker version (ADR-005);
	// detect it so restore only loads compatible snapshots and everything
	// else cold boots.
	fcVersion, err := deps.detectFC(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
	}

	mgr := fcvm.NewManager(
		wire.ExecRunner{},
		fcvm.NewJailerVMM(fcvm.JailChrootBase, 30*time.Second),
		fcvm.Paths{Kernel: cfg.KernelPath},
		fcVersion,
		log,
	)
	log.Info("vmmd ready", "fc_version", fcVersion, "max_slots", fcvm.MaxSlots,
		"uid_lo", fcvm.JailUIDBase, "uid_hi", fcvm.JailUIDMax)

	// Ops + listener: unix socket with ADR-015 mode 0660 group `faas`.
	ops := wire.NewOpsMetrics("vmmd")
	lis, err := deps.listen(cfg.SocketPath, cfg.OwnerUser)
	if err != nil {
		return err
	}
	gsrv := grpc.NewServer()
	impl := vmmdgrpc.New(mgr, ops, fcVersion, log)
	impl.Register(gsrv)

	// Optional /metrics endpoint.
	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle(metricsPath, ops.Handler())
		httpSrv = &http.Server{
			Addr:    cfg.MetricsAddr,
			Handler: mux,
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
		log.Info("grpc listening", "socket", cfg.SocketPath, "service", vmmdpb.Vmmd_ServiceDesc.ServiceName)
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
	//nolint:contextcheck // shutdown context must outlive caller ctx (which is already Done); detached from caller per gRPC + net/http contract.
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gsrv.GracefulStop()
	if httpSrv != nil {
		_ = httpSrv.Shutdown(stopCtx)
	}
	_ = lis.Close()
	return nil
}
