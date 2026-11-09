//go:build !linux

package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

func startProfilingReceiver(context.Context, *slog.Logger, *fcvm.Manager, state.Store, *fcvm.JailerVMM, prometheus.Registerer) error {
	return nil
}
