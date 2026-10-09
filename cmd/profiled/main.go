// Command profiled parses profiles outside the privileged VM broker and
// exports them to the operator's tenant-enabled Pyroscope backend (ADR-819).
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
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

func main() { wire.Daemon("profiled", run) }

func run(ctx context.Context, log *slog.Logger) error {
	if err := role.Require("profiled", role.FromConfig("", "FAAS_PROFILED_ROLE"), role.RoleSingleBox, role.RoleComputeOnly); err != nil {
		return err
	}
	var backend profiling.Backend
	if os.Getenv("FAAS_PROFILING_ENABLED") == "1" {
		var err error
		backend, err = profiling.NewPyroscope(os.Getenv("FAAS_PYROSCOPE_URL"), os.Getenv("FAAS_PYROSCOPE_TOKEN"))
		if err != nil {
			return err
		}
	}
	path := os.Getenv("FAAS_PROFILE_SOCKET")
	if path == "" {
		path = api.ProfileDefaultSocket
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("profile socket path exists and is not a socket")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	// Restrict the socket to this service account and root vmmd.
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	ops := wire.NewOpsMetrics("profiled")
	service := profiling.NewService(backend, ops.Registry())

	healthListener, err := net.Listen("tcp", api.ProfileHealthListen)
	if err != nil {
		return fmt.Errorf("profile health listener: %w", err)
	}
	defer func() { _ = healthListener.Close() }()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", ops.Handler())
	var ready atomic.Bool
	ready.Store(backend == nil)
	wire.ControlMuxLite(mux, ready.Load, func() string {
		if ready.Load() {
			return ""
		}
		return "profile backend unavailable"
	})
	go profileReadiness(ctx, backend, &ready, ops)
	health := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() {
		if err := health.Serve(healthListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("profile health listener stopped")
		}
	}()
	defer func() { _ = health.Close() }()
	server := grpc.NewServer(grpc.MaxRecvMsgSize(api.ProfileMaxFrameBytes+api.ProfileRPCOverheadBytes), grpc.MaxConcurrentStreams(api.ProfileMaxConcurrentUploads))
	profiling.Register(server, service)
	go func() { <-ctx.Done(); server.Stop() }()
	log.Info("profile service started", "enabled", backend != nil)
	if err := server.Serve(rootListener{Listener: listener}); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("profile service: %w", err)
	}
	return nil
}

func profileReadiness(ctx context.Context, backend profiling.Backend, ready *atomic.Bool, ops *wire.OpsMetrics) {
	ticker := time.NewTicker(api.ProfileTransportTimeout)
	defer ticker.Stop()
	for {
		healthy := true
		if checker, ok := backend.(interface{ Ready(context.Context) bool }); ok {
			probe, cancel := context.WithTimeout(ctx, api.ProfileTransportTimeout)
			healthy = checker.Ready(probe)
			cancel()
		}
		ready.Store(healthy)
		reason := ""
		if !healthy {
			reason = "profile backend unavailable"
		}
		ops.MarkReady("profiled", healthy, reason)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
