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
	"github.com/onebox-faas/faas/pkg/api"
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
	Profile api.ExecutionProfile `json:"profile,omitempty"`
	Kind    string               `json:"kind"`
	Version int                  `json:"version"`
}

// listenExecutionHook binds AF_VSOCK on VMADDR_CID_ANY and returns a regular
// net.Listener so the protocol path can be tested without a vsock device.
func listenExecutionHook() (net.Listener, error) {
	return listenGuestVsock(VsockExecutionPort)
}

// serveExecutionOnce accepts exactly one host connection and never reuses
// the listener. The caller owns listener lifetime and VM poweroff.
func serveExecutionOnce(ctx context.Context, ln net.Listener, handler executionproto.Handler) error {
	if ln == nil || handler == nil {
		return errors.New("execution listener is not configured")
	}
	return serveExecutionOnceWithBroker(ctx, ln, func(ctx context.Context, req executionproto.Request, stdout, stderr *executionproto.OutputWriter, _ executionproto.OutboundBroker) (executionproto.Result, error) {
		return handler(ctx, req, stdout, stderr)
	})
}

func serveExecutionOnceWithBroker(ctx context.Context, ln net.Listener, handler executionproto.HandlerWithBroker) error {
	if ln == nil || handler == nil {
		return errors.New("execution listener is not configured")
	}
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("execution vsock accept: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return executionproto.ServeWithBroker(ctx, conn, handler)
}

// listenExecutionResumeHook is a seam so protocol tests do not bind AF_VSOCK.
var listenExecutionResumeHook = func(log *slog.Logger) error {
	return listenResumeHook(log)
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
	marker, err := os.ReadFile(ExecutionManifestPath)
	if err != nil {
		_ = poweroffExecution()
		return err
	}
	if err := validateExecutionManifest(marker); err != nil {
		_ = poweroffExecution()
		return err
	}
	var manifest executionManifest
	if err := json.Unmarshal(marker, &manifest); err != nil {
		_ = poweroffExecution()
		return err
	}
	ln, err := listenExecutionHook()
	if err != nil {
		log.Warn("execution listener unavailable", "err", err)
		_ = poweroffExecution()
		return err
	}
	defer func() { _ = ln.Close() }()
	runExecutor := executor.NewWithProfile(manifest.Profile)
	// ADR-990: Runs stream outbound calls through the loopback relay when
	// vmmd advertises it per request; the broker helper stays the fallback.
	if err := startOutboundRelay(log); err != nil {
		log.Warn("execution outbound relay unavailable", "err", err)
	} else {
		runExecutor.WithOutboundRelay(OutboundRelayEndpoint)
	}
	// Bind the resume hook last, after the execution listener and relay. A
	// payload-free runtime snapshot is captured once the host's resume-hook
	// probe is acknowledged, so the acknowledgement proves the captured guest
	// is fully set up and waiting for its one request (ADR-986). On every
	// restore vmmd triggers the hook before dispatch, which re-seeds entropy
	// and steps the clock (spec §11 V6) so Runs restored from one snapshot
	// never share RNG state. A cold boot has fresh entropy and tolerates a
	// missing AF_VSOCK listener here.
	if err := listenExecutionResumeHook(log); err != nil {
		log.Warn("execution resume listener unavailable", "err", err)
	}
	serveErr := serveExecutionOnceWithBroker(context.Background(), ln, runExecutor.HandleWithBroker)
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
	if !marker.Profile.Valid() {
		return fmt.Errorf("unknown execution profile %q", marker.Profile)
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
