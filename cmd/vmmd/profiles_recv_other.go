//go:build !linux

package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
)

func startProfilingReceiver(context.Context, *slog.Logger, *fcvm.Manager, state.Store, *fcvm.JailerVMM) error {
	return nil
}
