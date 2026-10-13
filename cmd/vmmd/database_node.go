package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// databaseMethods are the only RPCs a database node serves.
var databaseMethods = map[string]bool{
	vmmdpb.Vmmd_CreateDatabaseVolume_FullMethodName: true,
	vmmdpb.Vmmd_DeleteDatabaseVolume_FullMethodName: true,
	vmmdpb.Vmmd_ListDatabaseVolumes_FullMethodName:  true,
	vmmdpb.Vmmd_GrowDatabaseVolume_FullMethodName:   true,
	vmmdpb.Vmmd_PruneDatabaseArchive_FullMethodName: true,
	vmmdpb.Vmmd_DatabaseArchiveBytes_FullMethodName: true,
	vmmdpb.Vmmd_StartDatabase_FullMethodName:        true,
	vmmdpb.Vmmd_StopDatabase_FullMethodName:         true,
	vmmdpb.Vmmd_DatabaseControl_FullMethodName:      true,
	vmmdpb.Vmmd_DatabaseSession_FullMethodName:      true,
}

func databaseOnlyUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if !databaseMethods[info.FullMethod] {
		return nil, status.Error(codes.Unimplemented, "database node: only database RPCs are served")
	}
	return handler(ctx, req)
}

func databaseOnlyStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if !databaseMethods[info.FullMethod] {
		return status.Error(codes.Unimplemented, "database node: only database RPCs are served")
	}
	return handler(srv, ss)
}

// databaseNodeVerifier admits the two callers of a database node: schedd's
// database scheduler and pgproxyd. The RPCs check which of them each call
// accepts.
func databaseNodeVerifier() wire.NodeVerifier {
	v := wire.NewInmemNodeVerifier()
	v.Set([]string{"schedd.faas", "pgproxyd.faas"})
	return v
}

// runDatabaseNode is vmmd on a database node (ADR-1027). It runs database
// VMs and nothing else: no compute_nodes registration (so schedd never
// places an app VM here), no tenant network, no snapshots. The firecracker
// jails are its own (JailBase, cgroup parent and the whole jail uid range).
func runDatabaseNode(ctx context.Context, cfg *Config, ops *wire.OpsMetrics, log *slog.Logger, deps runDeps) error {
	if cfg.DatabaseVMs == nil {
		return errors.New("vmmd: role database needs [database_vms]")
	}
	leases := deps.databaseLeases
	if leases == nil {
		leases = fcvm.NewAllocator()
	}
	rt, images, err := newDatabaseRuntime(cfg.DatabaseVMs, leases)
	if err != nil {
		return err
	}
	var probe wire.ReadyzProbe
	serving := probe.Register()
	basesBuilt := probe.Register()
	go buildDatabaseBases(ctx, rt, cfg.DatabaseVMs, images, basesBuilt, log)

	serverTLS, err := cfg.LoadServerTLSWithVerifier(databaseNodeVerifier())
	if err != nil {
		return fmt.Errorf("vmmd: load server TLS: %w", err)
	}
	gsrv := grpc.NewServer(append(append(
		wire.ServerCredsOrEmpty(serverTLS),
		wire.TraceServerOptions()...),
		grpc.ChainUnaryInterceptor(databaseOnlyUnary),
		grpc.ChainStreamInterceptor(databaseOnlyStream),
	)...)
	vmmdgrpc.New(nil, ops, "", log).WithDatabases(rt).Register(gsrv)
	listen := deps.listen
	if listen == nil {
		listen = wire.ListenAs
	}
	target := cfg.ResolveListenTarget()
	lis, err := listen(ctx, target, serverTLS, cfg.OwnerUser)
	if err != nil {
		return fmt.Errorf("vmmd: listen %s: %w", target, err)
	}

	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(ops.Registry(), promhttp.HandlerOpts{}))
		wire.ControlMuxLite(mux, probe.ReadyFunc(), probe.ReasonFunc())
		srv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("vmmd: metrics http", "err", err)
			}
		}()
		defer func() { _ = srv.Close() }()
	}

	// systemd learns "ready" once RPCs are served; /readyz additionally
	// waits for the bases, which can take minutes on a fresh node.
	stopNotify := daemonunit.NotifyReadyWhen(ctx, func() bool { r, _ := serving.Report(); return r })
	defer stopNotify()
	stopWatchdog := daemonunit.WatchdogFromEnv(ctx, func() bool { return true })
	defer stopWatchdog()

	serveErr := make(chan error, 1)
	go func() {
		log.Info("vmmd: database node serving", "addr", target, "images", len(images), "bases", len(cfg.DatabaseVMs.Bases))
		serving.Set(true, "")
		serveErr <- gsrv.Serve(lis)
	}()
	select {
	case err := <-serveErr:
		return fmt.Errorf("vmmd: serve: %w", err)
	case <-ctx.Done():
	}
	stopped := make(chan struct{})
	go func() { gsrv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		gsrv.Stop()
	}
	// Database VMs keep running across a vmmd restart; Recover adopts or
	// cleans them on the next start.
	return nil
}

var _ dbvm.Leaser = (*fcvm.Allocator)(nil)
