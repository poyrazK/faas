//go:build linux

package main

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// StartStatelessAdvisoryReceiver binds the ADR-047 advisory port on every VM.
// Without an apid advisory client the Manager drops batches, so the port is
// still bound: the guest's stream then ends cleanly instead of being refused.
func StartStatelessAdvisoryReceiver(log *slog.Logger, mgr *fcvm.Manager, jailer *fcvm.JailerVMM) error {
	if jailer == nil {
		return fmt.Errorf("stateless advisory vsock: jailer is required")
	}
	var (
		forward  statelessAdvisoryForward
		identity instanceIdentity
	)
	if mgr != nil {
		forward = mgr.ForwardStatelessAdvisory
		identity = mgr.InstanceIdentity
	}
	r := newStatelessAdvisoryReceiver(log, forward, identity)
	if err := jailer.RegisterGuestVsockStreamHandler(fcvm.VsockStatelessAdvisoryHostPort, r.handleGuestStream); err != nil {
		return fmt.Errorf("stateless advisory receiver register port %d: %w", fcvm.VsockStatelessAdvisoryHostPort, err)
	}
	r.log.Info("stateless advisory receiver registered", "vsock_host_port", fcvm.VsockStatelessAdvisoryHostPort)
	return nil
}
