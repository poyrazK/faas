//go:build !linux

// Non-linux stub for the liveness-probe starter (issue #554 / ADR-078).
// vmmd is a Linux-only daemon in production; this file exists so
// `go build ./cmd/vmmd/...` compiles on developer macOS for
// linting + non-KVM tests. The startLivenessLoopHelper body lives in
// liveness_recv.go (linux build tag). The signature matches the
// linux version exactly so cmd/vmmd/main.go compiles on either.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

type livenessActivityReader interface {
	Inflight(instanceID string) (int64, bool)
	LastAt(instanceID string) (time.Time, bool)
}

// startLivenessLoopHelper is a no-op stub on non-linux. Returns nil
// so Manager.startLivenessLoop treats it as "loop not started" (the
// production linux path spawns the goroutine and returns its cancel
// func).
func startLivenessLoopHelper(_ context.Context, _ *fcvm.Manager, _ *slog.Logger, _ string, _ int, _ string, _ fcvm.LivenessProbeConfig, _ string, _ livenessActivityReader) context.CancelFunc {
	return nil
}
