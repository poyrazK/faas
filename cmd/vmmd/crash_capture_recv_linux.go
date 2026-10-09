//go:build linux

package main

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/crashcapturewire"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// StartCrashCaptureReceiver binds the ADR-733 SDK trigger on every VM. It is
// always registered so the guest gets a clear not_enabled answer, not a
// refused connection, when crash snapshots are off on this node.
func StartCrashCaptureReceiver(log *slog.Logger, mgr *fcvm.Manager, store crashCaptureStore, enabled bool, jailer *fcvm.JailerVMM) (*CrashCaptureReceiver, error) {
	if jailer == nil {
		return nil, fmt.Errorf("crash capture vsock: jailer is required")
	}
	var identity instanceIdentity
	if mgr != nil {
		identity = mgr.InstanceIdentity
	}
	r := newCrashCaptureReceiver(log, store, identity, enabled)
	if err := jailer.RegisterGuestVsockStreamHandler(crashcapturewire.Port, r.handleGuestStream); err != nil {
		return nil, fmt.Errorf("crash capture receiver register port %d: %w", crashcapturewire.Port, err)
	}
	r.log.Info("crash capture receiver registered", "vsock_host_port", crashcapturewire.Port, "enabled", enabled && store != nil)
	return r, nil
}
