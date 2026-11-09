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
	"github.com/onebox-faas/faas/pkg/netns"
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
	// Network is the session's network mode (§6): api.SandboxNetworkNone
	// boots it without a network, api.SandboxNetworkRegistries with
	// DNS-gated egress to package registries and git hosts on TCP 443.
	Network string
	// ImageKey boots a seller image (a materialized job image, full rootfs)
	// as drive1 in place of LayerKey; vmmd stages /etc/faas/sandbox.json
	// on the VM's private copy, so the customer image is never changed.
	ImageKey string
	// StartCmd, ReadyCmd and ReadyTimeout are a template's commands, staged
	// in sandbox.json with ImageKey: guest-init listens only once the ready
	// command passes, so the wait for the port grows by ReadyTimeout.
	StartCmd     string
	ReadyCmd     string
	ReadyTimeout time.Duration
}

// WakeSandbox cold-boots one sandbox session VM on the execution-only path
// (a fresh jail, cgroup and lease; a network only in registries mode) and
// returns once
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
	registries := req.Network == api.SandboxNetworkRegistries
	if !registries && req.Network != api.SandboxNetworkNone {
		return nil, fmt.Errorf("fcvm: sandbox network mode %q is not supported", req.Network)
	}
	dialer, ok := m.vmm.(SandboxDialer)
	if !ok {
		return nil, errors.New("fcvm: the vmm cannot dial sandbox sessions")
	}
	layer := req.LayerKey
	var manifest string
	if req.ImageKey != "" {
		layer = req.ImageKey
		encoded, err := sandboxManifest(req.StartCmd, req.ReadyCmd, req.ReadyTimeout)
		if err != nil {
			return nil, fmt.Errorf("fcvm: sandbox manifest: %w", err)
		}
		manifest = string(encoded)
	} else if req.StartCmd != "" || req.ReadyCmd != "" {
		return nil, errors.New("fcvm: sandbox commands need an image drive")
	}
	inst, err := m.Wake(ctx, WakeRequest{
		Instance: req.Instance, ExecutionOnly: true, sandboxID: req.SandboxID, sandboxRegistries: registries,
		sandboxManifest: manifest, AccountID: req.AccountID, Plan: req.Plan,
		BaseKey: req.BaseKey, LayerKey: layer, VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
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
	if inst == nil || !inst.ExecutionOnly || inst.SandboxID != req.SandboxID || inst.Lease.Networkless == registries ||
		registries && (!inst.Net.SandboxRegistries || !inst.Net.DNSGated) {
		return destroy(fmt.Errorf("fcvm: sandbox wake did not produce a %s sandbox VM", req.Network))
	}
	if err := awaitSandboxListener(ctx, dialer, req.Instance, agentRunDialWindow+req.ReadyTimeout); err != nil {
		return destroy(err)
	}
	return inst, nil
}

// awaitSandboxListener dials the guest's sandbox port until it answers. Wake
// returns once Firecracker is up, before guest-init listens. The probe
// connection carries no request; the guest drops it.
func awaitSandboxListener(ctx context.Context, dialer SandboxDialer, instance string, window time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, window)
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

// networkless reports whether a wake boots without a tenant network: every
// execution-only VM except a sandbox session in registries mode.
func (r WakeRequest) networkless() bool { return r.ExecutionOnly && !r.sandboxRegistries }

// applySandboxRegistriesEgress is a registries-mode sandbox's egress
// (ADR-20261027090013 §6, under ADR-361 and ADR-373): DNS pinned to the
// registries-only resolver, new TCP only to addresses it answered and only on
// 443, and the plan's connection rates. No bridge service, database or
// extra port is reachable.
func applySandboxRegistriesEgress(nc *netns.Config, plan api.Plan) {
	nc.SandboxRegistries = true
	// The tenant is root in the guest (§10): the netns accepts only the
	// fixed MAC and GuestIP from the tap.
	nc.GuestMAC = netns.SandboxGuestMAC
	nc.DNSGated = true
	nc.EgressPorts = []uint16{443}
	if lim, ok := api.LimitsFor(plan); ok {
		nc.EgressConnRate, nc.EgressConnBurst = lim.EgressNewConnPerSecond, lim.EgressNewConnBurst
		nc.EgressDestConnRate, nc.EgressDestConnBurst = lim.EgressNewConnPerDestPerSecond, lim.EgressNewConnPerDestBurst
	}
}
