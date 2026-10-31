package fcvm

// ADR-732 fork exec: run one command inside a running quarantined fork over
// the guest's host-only fork exec vsock port. Only a live instance whose
// lease is quarantined qualifies, so no serving instance can ever be
// reached this way.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// ErrForkExecNotAFork refuses fork exec on anything but a live quarantined
// fork instance.
var ErrForkExecNotAFork = errors.New("fcvm: fork exec: instance is not a running fork")

// ForkExecDialer is the VMM capability behind fork exec.
type ForkExecDialer interface {
	DialForkExec(context.Context, Lease) (*AppTaskSession, error)
}

// DialForkExec connects to the fork exec port of a running guest. Unlike
// DialAppTask the session never destroys the VM.
func (v *JailerVMM) DialForkExec(ctx context.Context, lease Lease) (*AppTaskSession, error) {
	if v == nil || lease.Instance == "" {
		return nil, errors.New("fcvm: dial fork exec: no vmm or instance")
	}
	sock := v.vsockUDSSock(lease.Instance)
	dialCtx, cancel := context.WithTimeout(ctx, appTaskDialWindow)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("fcvm: dial fork exec vsock uds %s: %w", sock, err)
	}
	if err := completeVsockConnect(ctx, conn, apptaskproto.ForkExecVsockPort); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("fcvm: fork exec connect: %w", err)
	}
	return NewAppTaskSession(conn)
}

// ExecInFork runs req inside the running fork instance and returns its
// bounded result. The command is limited by req.TimeoutSeconds.
func (m *Manager) ExecInFork(ctx context.Context, instance string, req apptaskproto.Request) (apptaskproto.Result, error) {
	var zero apptaskproto.Result
	if err := req.Validate(); err != nil {
		return zero, err
	}
	if m == nil || m.vmm == nil {
		return zero, ErrAppTaskNotConfigured
	}
	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok || inst == nil || !inst.Lease.Quarantine || inst.AppTaskOnly {
		return zero, ErrForkExecNotAFork
	}
	dialer, ok := m.vmm.(ForkExecDialer)
	if !ok {
		return zero, ErrAppTaskNotConfigured
	}
	// The guest enforces the command timeout; leave room for the result.
	requestCtx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second+10*time.Second)
	defer cancel()
	session, err := dialer.DialForkExec(requestCtx, inst.Lease)
	if err != nil {
		return zero, err
	}
	defer func() { _ = session.Destroy(context.WithoutCancel(ctx)) }()
	return session.Execute(requestCtx, req)
}
