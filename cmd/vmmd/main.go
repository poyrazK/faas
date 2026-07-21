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
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"filippo.io/age"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/secretbox"
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
	// hostKey plumbing — function-typed so tests can drive first-boot
	// (LoadHostKey returns ErrHostKeyNotFound → GenerateAndSaveHostKey)
	// and restart (LoadHostKey returns id) paths without touching disk.
	loadHostKey    func(path string) (*age.X25519Identity, error)
	genAndSaveKey  func(path string) (*age.X25519Identity, error)
	writeRecipient func(path string, id *age.X25519Identity) error
}

func defaultDeps() runDeps {
	return runDeps{
		configPath:     envOrConfig("FAAS_VMMD_CONFIG", "/etc/faas/vmmd.toml"),
		detectFC:       fcvm.DetectFirecrackerVersion,
		listen:         wire.ListenOrRecreateByName,
		loadHostKey:    secretbox.LoadHostKey,
		genAndSaveKey:  secretbox.GenerateAndSaveHostKey,
		writeRecipient: secretbox.WriteRecipientFile,
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
	log.Info("config", "socket", cfg.SocketPath, "kernel", cfg.KernelPath,
		"metrics_addr", cfg.MetricsAddr)

	// Fill in host-key defaults if a test passed a zero-value runDeps
	// without these. The other deps (configPath, detectFC, listen) are
	// not defaulted here — they're test seams where nil is meaningful
	// (e.g. TestRun_BadConfigPath passes configPath = a directory).
	if deps.loadHostKey == nil {
		deps.loadHostKey = secretbox.LoadHostKey
	}
	if deps.genAndSaveKey == nil {
		deps.genAndSaveKey = secretbox.GenerateAndSaveHostKey
	}
	if deps.writeRecipient == nil {
		deps.writeRecipient = secretbox.WriteRecipientFile
	}

	// Snapshots are pinned to the running Firecracker version (ADR-005);
	// detect it so restore only loads compatible snapshots and everything
	// else cold boots.
	fcVersion, err := deps.detectFC(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
	}

	// Host-key lifecycle (ADR-020 / spec §11 G2). Without this, the
	// Manager refuses to wake any app that PUT a secret (Manager.Wake
	// returns ErrNoHostKey). vmmd is the only writer to the on-disk
	// key — apid reads the public recipient to seal, builderd reads
	// it to seal build-time env, and the wake path inside vmmd unseals
	// with the private identity. The first-boot branch generates a
	// fresh X25519 identity; the restart branch loads the existing
	// one and re-emits the public recipient file (idempotent).
	hostID, keyPath, pubPath, err := loadOrGenerateHostIdentity(deps,
		envOrConfig("FAAS_HOST_KEY_PATH", secretbox.DefaultHostKeyPath),
		envOrConfig("FAAS_HOST_AGE_RECIPIENT_PATH", secretbox.DefaultHostAgeRecipientPath),
	)
	if err != nil {
		return err
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
	mgr.SetHostIdentity(hostID)
	log.Info("vmmd ready", "fc_version", fcVersion, "max_slots", fcvm.MaxSlots,
		"uid_lo", fcvm.JailUIDBase, "uid_hi", fcvm.JailUIDMax,
		"host_key_path", keyPath, "recipient_path", pubPath,
		"recipient", hostID.Recipient().String())

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

// loadOrGenerateHostIdentity implements the G2 host-key lifecycle:
//
//  1. Try LoadHostKey(path).
//  2. On ErrHostKeyNotFound (first boot) → GenerateAndSaveHostKey(path).
//  3. Always WriteRecipientFile(pubPath, id) so apid / builderd have
//     a fresh public recipient to seal against on every startup.
//
// Returns the identity plus the resolved paths so the caller can log
// them. Extracted so tests can drive the lifecycle without booting
// the full gRPC + listener stack.
func loadOrGenerateHostIdentity(deps runDeps, keyPath, pubPath string) (*age.X25519Identity, string, string, error) {
	id, err := deps.loadHostKey(keyPath)
	if errors.Is(err, secretbox.ErrHostKeyNotFound) {
		id, err = deps.genAndSaveKey(keyPath)
	}
	if err != nil {
		return nil, keyPath, pubPath, fmt.Errorf("vmmd: host key (%s): %w", keyPath, err)
	}
	if err := deps.writeRecipient(pubPath, id); err != nil {
		return nil, keyPath, pubPath, fmt.Errorf("vmmd: write recipient (%s): %w", pubPath, err)
	}
	return id, keyPath, pubPath, nil
}
