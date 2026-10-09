//go:build !linux

package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func startTraceReceiver(context.Context, *slog.Logger, *fcvm.Manager, state.Store, *fcvm.JailerVMM) (func(), error) {
	return func() {}, nil
}
