//go:build !linux

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func StartRuntimeConfigReceiver(context.Context, *slog.Logger, *fcvm.Manager, runtimeConfigStore, *fcvm.JailerVMM) (*runtimeConfigReceiver, error) {
	return nil, fmt.Errorf("runtime config vsock requires Linux")
}
