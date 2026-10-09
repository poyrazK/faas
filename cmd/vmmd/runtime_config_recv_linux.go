//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// StartRuntimeConfigReceiver registers the instance-bound host side of the
// guest metadata endpoint. A missing store keeps the transport available but
// returns config_unavailable, which lets images roll out before the control
// plane has enabled live configuration reads.
func StartRuntimeConfigReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store runtimeConfigStore, jailer *fcvm.JailerVMM) (*runtimeConfigReceiver, error) {
	if ctx == nil {
		return nil, errors.New("runtime config vsock: context is required")
	}
	if jailer == nil {
		return nil, errors.New("runtime config vsock: jailer is required")
	}
	if log == nil {
		log = slog.Default()
	}
	r := &runtimeConfigReceiver{ctx: ctx, log: log, mgr: mgr, store: store}
	if err := jailer.RegisterGuestVsockStreamHandler(VsockRuntimeConfigHostPort, r.handleGuestStream); err != nil {
		return nil, fmt.Errorf("runtime config receiver register port %d: %w", VsockRuntimeConfigHostPort, err)
	}
	if err := jailer.RegisterEnvironmentQualificationRestoreStreamHandler(VsockRuntimeConfigHostPort, r.handleQualificationRestoreStream); err != nil {
		return nil, fmt.Errorf("qualification runtime config receiver register port %d: %w", VsockRuntimeConfigHostPort, err)
	}
	log.Info("runtime config receiver registered", "vsock_host_port", VsockRuntimeConfigHostPort, "transport", "firecracker_uds", "enabled", store != nil)
	return r, nil
}
