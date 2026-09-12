// Command realtimed owns Gregale managed realtime connections. The public
// gateway forwards the reserved /__gregale/realtime/ namespace to this daemon;
// endpoint and management operations are available only on its Unix socket.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/wire"
)

const defaultSocket = "/run/faas/realtimed.sock"
const defaultHealthListen = "127.0.0.1:9107"

func main() {
	wire.Daemon("realtimed", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	observedRole := role.FromConfig(getenv("FAAS_REALTIME_ROLE", string(role.RoleSingleBox)), "FAAS_REALTIME_ROLE")
	if err := role.Require("realtimed", observedRole, role.RoleSingleBox, role.RoleComputeOnly); err != nil {
		return err
	}
	socketPath := getenv("FAAS_REALTIME_SOCKET", defaultSocket)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		return err
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	manager := realtime.NewManager(realtime.Config{
		MaxConnections:   envInt("FAAS_REALTIME_MAX_CONNECTIONS", 10_000),
		MaxMessageBytes:  int64(envInt("FAAS_REALTIME_MAX_MESSAGE_BYTES", 1<<20)),
		OutboundQueue:    envInt("FAAS_REALTIME_OUTBOUND_QUEUE", 64),
		Heartbeat:        envDuration("FAAS_REALTIME_HEARTBEAT", 30*time.Second),
		PongWait:         envDuration("FAAS_REALTIME_PONG_WAIT", 10*time.Second),
		WriteWait:        envDuration("FAAS_REALTIME_WRITE_WAIT", 5*time.Second),
		MaxConnectionAge: envDuration("FAAS_REALTIME_MAX_AGE", 24*time.Hour),
		CallbackTimeout:  envDuration("FAAS_REALTIME_CALLBACK_TIMEOUT", 30*time.Second),
	}, realtime.HTTPHooks{})
	defer func() { _ = manager.Close() }()

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if err := os.Chmod(socketPath, 0o660); err != nil {
		return err
	}
	healthListener, err := net.Listen("tcp", getenv("FAAS_REALTIME_HEALTH_LISTEN", defaultHealthListen))
	if err != nil {
		_ = listener.Close()
		return err
	}

	server := &http.Server{
		Handler:           manager.HTTPHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	healthServer := &http.Server{
		Handler:           manager.HTTPHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	serverErr := make(chan error, 2)
	go func() {
		log.Info("realtimed listening", "socket", socketPath)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()
	go func() {
		log.Info("realtimed health listening", "address", healthListener.Addr().String())
		if err := healthServer.Serve(healthListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = manager.Close()
		_ = healthServer.Shutdown(shutdownCtx)
		return server.Shutdown(shutdownCtx)
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
