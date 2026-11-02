package fcvm

// Sandbox session calls (ADR-20261027090013 §4): vmmd dials a session VM's
// sandbox port for each operation. Only a live VM booted for that very
// session qualifies, so a sandbox ID never reaches an app, run or other
// session's VM.

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// ErrSandboxNotFound is an instance that is not a live VM of the sandbox.
var ErrSandboxNotFound = errors.New("fcvm: no live VM of that sandbox session")

// SandboxDialer is the VMM capability behind sandbox calls.
type SandboxDialer interface {
	DialSandbox(ctx context.Context, instance string) (net.Conn, error)
}

// DialSandbox connects to a running guest's sandbox port. The connection
// carries one sandboxproto operation; the VM outlives it.
func (v *JailerVMM) DialSandbox(ctx context.Context, instance string) (net.Conn, error) {
	if v == nil || instance == "" {
		return nil, errors.New("fcvm: dial sandbox: no vmm or instance")
	}
	sock := v.vsockUDSSock(instance)
	dialCtx, cancel := context.WithTimeout(ctx, appTaskDialWindow)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("fcvm: dial sandbox vsock uds %s: %w", sock, err)
	}
	if err := completeVsockConnect(ctx, conn, sandboxproto.VsockPort); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("fcvm: sandbox connect: %w", err)
	}
	return conn, nil
}

// DialSandboxSession connects to instance's sandbox port when instance is a
// live VM booted for sandboxID.
func (m *Manager) DialSandboxSession(ctx context.Context, instance, sandboxID string) (net.Conn, error) {
	if m == nil || sandboxID == "" {
		return nil, ErrSandboxNotFound
	}
	m.mu.Lock()
	inst, ok := m.live[instance]
	ours := ok && inst != nil && inst.SandboxID == sandboxID
	m.mu.Unlock()
	if !ours {
		return nil, ErrSandboxNotFound
	}
	dialer, ok := m.vmm.(SandboxDialer)
	if !ok {
		return nil, errors.New("fcvm: the vmm cannot dial sandbox sessions")
	}
	return dialer.DialSandbox(ctx, instance)
}
