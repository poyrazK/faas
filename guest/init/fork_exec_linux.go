//go:build linux

package main

// ADR-732 fork exec. Every app VM listens on a guest vsock port that only
// the host can reach; vmmd dials it only for a quarantined fork, so a
// command runs inside the debug copy of production and never in a serving
// instance. One command runs at a time, in the app's working directory and
// user, with the manifest environment and no secrets (the fork's secrets
// files were scrubbed at restore).

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func startForkExecListener(log *slog.Logger) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("fork exec vsock socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: VsockAppTaskBindCID, Port: apptaskproto.ForkExecVsockPort}); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("fork exec vsock bind port %d: %w", apptaskproto.ForkExecVsockPort, err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("fork exec vsock listen: %w", err)
	}
	f := os.NewFile(uintptr(fd), "fork-exec-vsock")
	ln, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("fork exec vsock listener: %w", err)
	}
	go serveForkExec(log, ln)
	log.Info("fork exec listener started", "vsock_port", apptaskproto.ForkExecVsockPort)
	return nil
}

// serveForkExec handles one session at a time; each session runs one command.
func serveForkExec(log *slog.Logger, ln net.Listener) {
	handler := forkExecHandler()
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Warn("fork exec listener stopped", "err", err)
			return
		}
		if err := apptaskproto.Serve(context.Background(), conn, handler); err != nil {
			log.Warn("fork exec session", "err", err)
		}
		_ = conn.Close()
	}
}

func forkExecHandler() apptaskproto.Handler {
	return func(ctx context.Context, req apptaskproto.Request, stdout, stderr *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
		//nolint:forbidigo // platform-owned app manifest, staged in the immutable deployment image
		f, err := os.Open(api.AppManifestPath)
		if err != nil {
			return appTaskInfraFailure("manifest_unavailable", "app manifest could not be loaded", 126), nil //nolint:nilerr // the protocol carries infrastructure failures as terminal results
		}
		manifest, manifestErr := api.ReadManifest(f)
		_ = f.Close()
		if manifestErr != nil {
			return appTaskInfraFailure("manifest_invalid", "app manifest is invalid", 126), nil //nolint:nilerr // the protocol carries infrastructure failures as terminal results
		}
		return executeAppTaskCommand(ctx, req, manifest, nil, nil, stdout, stderr)
	}
}
