//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"

	"github.com/onebox-faas/faas/guest/executor"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"golang.org/x/sys/unix"
)

const (
	// VsockExecutionPort is the guest-side listener reserved for one-shot
	// execution. It must mirror executionproto.VsockPort and fcvm's CONNECT
	// handshake; keep this local constant so guest-init stays independent of
	// the host VMM package.
	VsockExecutionPort            uint32 = executionproto.VsockPort
	VsockExecutionBindCID                = 0xffffffff
	ExecutionManifestPath                = "/etc/faas/execution.json"
	executionManifestRelativePath        = "etc/faas/execution.json"
	executionManifestKind                = "execution"
	executionManifestVersion             = 1
)

type executionManifest struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

// listenExecutionHook binds AF_VSOCK on VMADDR_CID_ANY and returns a regular
// net.Listener so the protocol path can be tested without a vsock device.
func listenExecutionHook() (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("execution vsock socket: %w", err)
	}
	addr := &unix.SockaddrVM{CID: VsockExecutionBindCID, Port: VsockExecutionPort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("execution vsock bind port %d: %w", VsockExecutionPort, err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("execution vsock listen: %w", err)
	}
	f := os.NewFile(uintptr(fd), "execution-vsock")
	ln, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("execution vsock listener: %w", err)
	}
	return ln, nil
}

// serveExecutionOnce accepts exactly one host connection and never reuses
// the listener. The caller owns listener lifetime and VM poweroff.
func serveExecutionOnce(ctx context.Context, ln net.Listener, handler executionproto.Handler) error {
	if ln == nil || handler == nil {
		return errors.New("execution listener is not configured")
	}
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("execution vsock accept: %w", err)
	}
	defer conn.Close()
	return executionproto.Serve(ctx, conn, handler)
}

var poweroffExecution = func() error {
	unix.Sync()
	return unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
}

// runExecutionGuest is intentionally total: even malformed requests,
// interpreter failures, and host disconnects lead to a VM poweroff. A
// disposable execution guest must not fall back into an app or idle state.
func runExecutionGuest(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	ln, err := listenExecutionHook()
	if err != nil {
		_ = poweroffExecution()
		return err
	}
	defer ln.Close()
	serveErr := serveExecutionOnce(context.Background(), ln, executor.New().Handle)
	if serveErr != nil {
		log.Warn("execution guest exchange failed", "err", serveErr)
	}
	if powerErr := poweroffExecution(); powerErr != nil {
		if serveErr != nil {
			return errors.Join(serveErr, fmt.Errorf("execution guest poweroff: %w", powerErr))
		}
		return fmt.Errorf("execution guest poweroff: %w", powerErr)
	}
	return serveErr
}

// validateExecutionManifest is kept separate from decideMode for focused
// tests and to make the marker's strictness explicit. The marker is platform
// owned; a malformed marker fails closed rather than silently booting an app.
func validateExecutionManifest(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker executionManifest
	if err := decoder.Decode(&marker); err != nil {
		return err
	}
	if marker.Kind != executionManifestKind || marker.Version != executionManifestVersion {
		return fmt.Errorf("kind=%q version=%d", marker.Kind, marker.Version)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing data")
		}
		return err
	}
	return nil
}
