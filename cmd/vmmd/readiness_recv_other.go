//go:build !linux

package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// vmmd runs only on Linux; this stub keeps developer builds and tooling
// working on other platforms.
func startAppReadinessProbeLoop(context.Context, *slog.Logger, *events.Platform, string, string, fcvm.ReadinessProbeConfig, string) {
}
