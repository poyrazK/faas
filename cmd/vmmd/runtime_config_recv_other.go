//go:build !linux

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

const VsockRuntimeConfigHostPort uint32 = fcvm.VsockRuntimeConfigHostPort

type runtimeConfigStore interface{}

type runtimeConfigReceiver struct{}

func StartRuntimeConfigReceiver(context.Context, *slog.Logger, *fcvm.Manager, runtimeConfigStore, *fcvm.JailerVMM) (*runtimeConfigReceiver, error) {
	return nil, fmt.Errorf("runtime config vsock requires Linux")
}

func (*runtimeConfigReceiver) Close() {}
