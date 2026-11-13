//go:build !linux

package main

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func StartStatelessAdvisoryReceiver(*slog.Logger, *fcvm.Manager, *fcvm.JailerVMM) error {
	return fmt.Errorf("stateless advisory vsock requires Linux")
}
