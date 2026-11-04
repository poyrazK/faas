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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

// SandboxWakeRequest is the machine for one sandbox session: the agent-base
// drive0, the platform sandbox layer (drive1) that holds
// /etc/faas/sandbox.json, and the size admission chose. No tenant data is in
// the image; every operation arrives later over SandboxCall.
type SandboxWakeRequest struct {
	Instance      string
	SandboxID     string
	AccountID     string
	Plan          api.Plan
	BaseKey       string
	LayerKey      string
	VcpuCount     int
	MemSizeMiB    int
	CPUMillicores int
	// QuietPauseAfter pauses the VM between calls (ADR-20261027090013 §5);
	// zero keeps it running.
	QuietPauseAfter time.Duration
}

// WakeSandbox cold-boots one sandbox session VM on the execution-only path
// (a fresh jail, cgroup and lease, no tenant network) and returns once
// guest-init's sandbox server answers, so the first call never races the
// boot. The VM lives until Destroy.
func (m *Manager) WakeSandbox(ctx context.Context, req SandboxWakeRequest) (*Instance, error) {
	if m == nil || m.vmm == nil {
		return nil, errors.New("fcvm: sandbox sessions are not configured")
	}
	if req.Instance == "" || req.SandboxID == "" || req.AccountID == "" || !req.Plan.Valid() || req.BaseKey == "" ||
		req.LayerKey == "" || req.VcpuCount <= 0 || req.MemSizeMiB <= 0 || req.CPUMillicores <= 0 || req.QuietPauseAfter < 0 {
		return nil, errors.New("fcvm: incomplete sandbox wake request")
	}
	dialer, ok := m.vmm.(SandboxDialer)
	if !ok {
		return nil, errors.New("fcvm: the vmm cannot dial sandbox sessions")
	}
	inst, err := m.Wake(ctx, WakeRequest{
		Instance: req.Instance, ExecutionOnly: true, sandboxID: req.SandboxID, AccountID: req.AccountID, Plan: req.Plan,
		BaseKey: req.BaseKey, LayerKey: req.LayerKey, VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
		CPUMillicores: req.CPUMillicores, QuietPauseAfter: req.QuietPauseAfter,
	})
	if err != nil {
		return nil, err
	}
	destroy := func(cause error) (*Instance, error) {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentRunDestroyTimeout)
		defer cancel()
		return nil, errors.Join(cause, m.Destroy(destroyCtx, req.Instance))
	}
	if inst == nil || !inst.ExecutionOnly || inst.SandboxID != req.SandboxID || !inst.Lease.Networkless {
		return destroy(errors.New("fcvm: sandbox wake did not produce a networkless sandbox VM"))
	}
	if err := awaitSandboxListener(ctx, dialer, req.Instance); err != nil {
		return destroy(err)
	}
	return inst, nil
}

// awaitSandboxListener dials the guest's sandbox port until it answers. Wake
// returns once Firecracker is up, before guest-init listens. The probe
// connection carries no request; the guest drops it.
func awaitSandboxListener(ctx context.Context, dialer SandboxDialer, instance string) error {
	waitCtx, cancel := context.WithTimeout(ctx, agentRunDialWindow)
	defer cancel()
	for {
		conn, err := dialer.DialSandbox(waitCtx, instance)
		if err == nil {
			return conn.Close()
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("fcvm: sandbox %s never listened: %w", instance, err)
		case <-time.After(executionDialStep):
		}
	}
}
