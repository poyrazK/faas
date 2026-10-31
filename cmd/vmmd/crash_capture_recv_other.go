//go:build !linux

package main

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func StartCrashCaptureReceiver(*slog.Logger, *fcvm.Manager, crashCaptureStore, bool, *fcvm.JailerVMM) (*CrashCaptureReceiver, error) {
	return nil, fmt.Errorf("crash capture vsock requires Linux")
}
