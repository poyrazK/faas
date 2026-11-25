//go:build linux

package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestHealthcheckPollWithoutDatagramVsockIsNotAWarning pins the agent eval of
// 2026-11-23: every Firecracker guest logged "WARN healthcheck poll
// unavailable ... no such device", which an agent and a diagnosis read as the
// reason an image HEALTHCHECK never passed. ENODEV is the expected absence of
// a datagram transport; other failures still warn.
func TestHealthcheckPollWithoutDatagramVsockIsNotAWarning(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logHealthcheckPollUnavailable(log, fmt.Errorf("healthcheck: vsock socket: %w", unix.ENODEV))
	if out.Len() != 0 {
		t.Fatalf("ENODEV logged at info or above: %s", out.String())
	}
	logHealthcheckPollUnavailable(log, fmt.Errorf("healthcheck: vsock bind 1029: %w", unix.EADDRINUSE))
	if !strings.Contains(out.String(), "level=WARN") {
		t.Fatalf("a bind failure was not a warning: %s", out.String())
	}
}
