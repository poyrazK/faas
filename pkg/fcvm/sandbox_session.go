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
	"slices"
	"strings"
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
	// DNS-gated egress to package registries and git hosts on TCP 443, and
	// api.SandboxNetworkWeb with DNS-gated egress to public names on TCP 80
	// and 443 through the tenant egress gateway (ADR-20261112121456).
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
	// Browser starts the sandbox browser as the session's start and ready
	// commands, after a cold boot or a restore (ADR-20261116182615 §1). Its
	// commands are vmmd's; a session with commands of its own or an image
	// drive cannot have it.
	Browser bool
	// EgressAllow and EgressDeny are a web-mode session's domain policy
	// (ADR-20261117091026 §3): the resolver's grant refuses a name the
	// policy does not allow. vmmd validated them.
	EgressAllow []string
	EgressDeny  []string
	// Snapshot restores the session from a warm snapshot CaptureSandboxSnapshot
	// took of the same drives and machine (ADR-20261027090013 §12), and
	// falls back to a cold boot (ADR-005). It was captured before the
	// template's commands, so a restored session gets them by
	// template_start. A registries or web session restores only a
	// networked snapshot, a none session only a networkless one.
	Snapshot *Snapshot
	// SessionCapture marks Snapshot as the session's own pause capture
	// (ADR-20261122132055 §2), taken after its commands and browser started:
	// a restore runs none of them again. A cold-boot fallback (ADR-005) runs
	// them as for a new session.
	SessionCapture bool
	// ForkedFrom is the source session of a fork restored from its fork
	// capture (ADR-20261122132055 §3). Once the guest answers, it is told,
	// so commands see GREGALE_FORKED_FROM and those that asked reseed.
	ForkedFrom string
	// Volume is a persistent session's workspace volume, a host path from
	// SandboxVolumes.Ensure (ADR-20261122132055 §4). guest-init mounts it at
	// /workspace. A warm snapshot has no such drive, so a session with a
	// volume cold-boots unless it restores its own capture.
	Volume string
}

// WakeSandbox cold-boots one sandbox session VM on the execution-only path
// (a fresh jail, cgroup and lease; a network only in registries or web mode) and
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
	var network string
	switch req.Network {
	case api.SandboxNetworkNone:
	case api.SandboxNetworkRegistries, api.SandboxNetworkWeb:
		network = req.Network
	default:
		return nil, fmt.Errorf("fcvm: sandbox network mode %q is not supported", req.Network)
	}
	networked := network != ""
	if req.SessionCapture && req.Snapshot == nil {
		return nil, errors.New("fcvm: a session capture restore needs its snapshot")
	}
	if req.Volume != "" && !req.SessionCapture {
		req.Snapshot = nil
	}
	if req.Snapshot != nil && req.Snapshot.Networkless == networked {
		return nil, fmt.Errorf("fcvm: a %s sandbox cannot restore this snapshot's network", req.Network)
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
	if req.Browser && (req.ImageKey != "" || req.StartCmd != "" || req.ReadyCmd != "") {
		return nil, errors.New("fcvm: the sandbox browser goes with a preset session, without commands of its own")
	}
	inst, err := m.Wake(ctx, WakeRequest{
		Instance: req.Instance, ExecutionOnly: true, sandboxID: req.SandboxID, sandboxNetwork: network,
		sandboxEgressAllow: req.EgressAllow, sandboxEgressDeny: req.EgressDeny,
		sandboxManifest: manifest, AccountID: req.AccountID, Plan: req.Plan,
		BaseKey: req.BaseKey, LayerKey: layer, VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
		CPUMillicores: req.CPUMillicores, QuietPauseAfter: req.QuietPauseAfter, Snapshot: req.Snapshot,
		sandboxVolume: req.Volume,
	})
	if err != nil {
		return nil, err
	}
	destroy := func(cause error) (*Instance, error) {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentRunDestroyTimeout)
		defer cancel()
		return nil, errors.Join(cause, m.Destroy(destroyCtx, req.Instance))
	}
	if inst == nil || !inst.ExecutionOnly || inst.SandboxID != req.SandboxID || inst.Lease.Networkless == networked ||
		networked && !inst.Net.DNSGated || inst.Net.SandboxRegistries != (network == api.SandboxNetworkRegistries) ||
		inst.Net.SandboxWeb != (network == api.SandboxNetworkWeb) {
		return destroy(fmt.Errorf("fcvm: sandbox wake did not produce a %s sandbox VM", req.Network))
	}
	if err := awaitSandboxListener(ctx, dialer, req.Instance, agentRunDialWindow+req.ReadyTimeout); err != nil {
		return destroy(err)
	}
	// A cold boot ran the commands from its manifest; a warm snapshot was
	// captured before them. The browser's commands have no manifest, so they
	// run here after either. A restored pause capture already runs them.
	switch {
	case req.SessionCapture && inst.Method == WakeRestore:
	case req.Browser:
		if err := startSandboxTemplate(ctx, dialer, sandboxBrowserCommands(req)); err != nil {
			return destroy(err)
		}
	case inst.Method == WakeRestore && (req.StartCmd != "" || req.ReadyCmd != ""):
		if err := startSandboxTemplate(ctx, dialer, req); err != nil {
			return destroy(err)
		}
	}
	if req.ForkedFrom != "" {
		m.markSandboxForked(ctx, dialer, req)
	}
	return inst, nil
}

// markSandboxForked tells a fork's guest its source. A guest-init from
// before the marker refuses the op; the fork runs on without it.
func (m *Manager) markSandboxForked(ctx context.Context, dialer SandboxDialer, req SandboxWakeRequest) {
	markCtx, cancel := context.WithTimeout(ctx, sandboxForkMarkTimeout)
	defer cancel()
	conn, err := dialer.DialSandbox(markCtx, req.Instance)
	if err == nil {
		var res sandboxproto.ForkedResult
		err = sandboxproto.Do(markCtx, conn, sandboxproto.Request{Op: sandboxproto.OpForked, ForkedFrom: req.ForkedFrom},
			func(uint32, []byte) error { return nil }, &res)
		_ = conn.Close()
		if err == nil {
			m.log.Info("fcvm: sandbox fork marked", "instance", req.Instance, "forked_from", req.ForkedFrom, "signaled", res.Signaled)
			return
		}
	}
	m.log.Warn("fcvm: sandbox fork not marked", "instance", req.Instance, "forked_from", req.ForkedFrom, "err", err)
}

// sandboxForkMarkTimeout bounds telling a fork's guest it is one.
const sandboxForkMarkTimeout = 5 * time.Second

// The sandbox browser's commands (ADR-20261116182615 §1): gregale-browser
// in agent-base starts Chromium and its CDP relay; its --wait polls the
// relay. vmmd owns them; no caller supplies them.
const (
	sandboxBrowserStartCmd     = "gregale-browser"
	sandboxBrowserReadyCmd     = "gregale-browser --wait 2"
	sandboxBrowserReadyTimeout = 45 * time.Second
	// sandboxBrowserPrewarmTimeout bounds the capture's prewarm.
	sandboxBrowserPrewarmTimeout = 90 * time.Second
	// sandboxBrowserPrewarmOutputBytes bounds the prewarm's output, which
	// nobody reads.
	sandboxBrowserPrewarmOutputBytes = 64 << 10
)

// sandboxBrowserCommands is req with the browser's commands.
func sandboxBrowserCommands(req SandboxWakeRequest) SandboxWakeRequest {
	req.StartCmd, req.ReadyCmd, req.ReadyTimeout = sandboxBrowserStartCmd, sandboxBrowserReadyCmd, sandboxBrowserReadyTimeout
	return req
}

// prewarmSandboxBrowser runs gregale-browser --prewarm in a capture's VM:
// it starts Chromium, waits for it, stops it and deletes its profile, so
// Chromium's files are in the snapshot's page cache and nothing that runs is
// (ADR-20261116182615 §2).
func prewarmSandboxBrowser(ctx context.Context, dialer SandboxDialer, instance string) error {
	ctx, cancel := context.WithTimeout(ctx, sandboxBrowserPrewarmTimeout+sandboxTemplateStartSlack)
	defer cancel()
	conn, err := dialer.DialSandbox(ctx, instance)
	if err != nil {
		return fmt.Errorf("fcvm: sandbox %s browser prewarm: %w", instance, err)
	}
	defer func() { _ = conn.Close() }()
	var res sandboxproto.ExecResult
	op := sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{sandboxBrowserStartCmd, "--prewarm"},
		TimeoutMillis: sandboxBrowserPrewarmTimeout.Milliseconds(), MaxOutputBytes: sandboxBrowserPrewarmOutputBytes}
	if err := sandboxproto.Do(ctx, conn, op, func(uint32, []byte) error { return nil }, &res); err != nil {
		return fmt.Errorf("fcvm: sandbox %s browser prewarm: %w", instance, err)
	}
	if res.ExitCode != 0 || res.TimedOut || res.Signal != 0 {
		return fmt.Errorf("fcvm: sandbox %s browser prewarm exited %d (signal %d, timed out %v)", instance, res.ExitCode, res.Signal, res.TimedOut)
	}
	return nil
}

// sandboxTemplateStartSlack is the time template_start may take beyond the
// ready timeout: the start command's launch and the answer.
const sandboxTemplateStartSlack = 10 * time.Second

// startSandboxTemplate runs a restored session's template commands and
// returns once the ready command passed.
func startSandboxTemplate(ctx context.Context, dialer SandboxDialer, req SandboxWakeRequest) error {
	startCtx, cancel := context.WithTimeout(ctx, req.ReadyTimeout+sandboxTemplateStartSlack)
	defer cancel()
	conn, err := dialer.DialSandbox(startCtx, req.Instance)
	if err != nil {
		return fmt.Errorf("fcvm: sandbox %s template start: %w", req.Instance, err)
	}
	defer func() { _ = conn.Close() }()
	op := sandboxproto.Request{Op: sandboxproto.OpTemplateStart, StartCmd: req.StartCmd, ReadyCmd: req.ReadyCmd,
		TimeoutMillis: req.ReadyTimeout.Milliseconds()}
	if err := sandboxproto.Do(startCtx, conn, op, func(uint32, []byte) error { return nil }, nil); err != nil {
		return fmt.Errorf("fcvm: sandbox %s template start: %w", req.Instance, err)
	}
	return nil
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
// execution-only VM except a sandbox session in registries or web mode.
func (r WakeRequest) networkless() bool { return r.ExecutionOnly && r.sandboxNetwork == "" }

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

// sandboxWebEgressPorts are the ports a web-mode session reaches
// (ADR-20261112121456 §2). QUIC stays dropped (ADR-361), so browsers fall
// back to TCP.
var sandboxWebEgressPorts = []uint16{80, 443}

// applySandboxWebEgress is a web-mode sandbox's egress (ADR-20261112121456
// §2, under ADR-361 and ADR-373): DNS pinned to the public-names resolver,
// new TCP only to addresses it answered and only on 80 and 443, and the
// plan's connection rates. Like registries mode, no bridge service, database
// or private service address is reachable.
func applySandboxWebEgress(nc *netns.Config, plan api.Plan) {
	nc.SandboxWeb = true
	// The tenant is root in the guest (ADR-20261027090013 §10).
	nc.GuestMAC = netns.SandboxGuestMAC
	nc.DNSGated = true
	nc.EgressPorts = slices.Clone(sandboxWebEgressPorts)
	if lim, ok := api.LimitsFor(plan); ok {
		nc.EgressConnRate, nc.EgressConnBurst = lim.EgressNewConnPerSecond, lim.EgressNewConnBurst
		nc.EgressDestConnRate, nc.EgressDestConnBurst = lim.EgressNewConnPerDestPerSecond, lim.EgressNewConnPerDestBurst
	}
}

// errSandboxWebNeedsGateway refuses a web-mode session on a node whose
// tenant egress leaves from its own address (ADR-20261112121456 §3).
var errSandboxWebNeedsGateway = errors.New("sandbox web egress needs the tenant egress gateway (FAAS_TENANT_EGRESS_IFACE), which this node does not route through")

// sandboxWebEgressRefusal reports why this node cannot boot a web-mode
// session, or nil. It fails closed: without DNS gating, or without the
// agent tunnel or the ADR-372 gateway unless the operator allowed node
// egress for a test fleet.
func (m *Manager) sandboxWebEgressRefusal() error {
	if m.dnsGatingOff {
		return errors.New("sandbox web egress needs DNS-gated egress, which this vmmd has switched off")
	}
	if m.sandboxWebNodeEgress || m.agentEgressOn() {
		// The agent tunnel carries the session (ADR-20261117185346), and
		// prepareAgentEgressWake refuses the boot when it cannot.
		return nil
	}
	if p := netns.ActiveHostPolicyForRender(); p == nil || strings.TrimSpace(p.TenantEgressIface) == "" {
		return errSandboxWebNeedsGateway
	}
	return nil
}

// SandboxTaskResult is a sandbox task command's end (ADR-20261116182615 §3):
// its exit code and standard output, capped at the task's limit.
type SandboxTaskResult struct {
	ExitCode  int
	Output    []byte
	TimedOut  bool
	Truncated bool
}

// RunSandboxTask runs argv in sandboxID's VM as the session's user, bounded
// by timeout, and returns its exit code and at most maxOutput bytes of its
// standard output. Standard error is discarded.
func (m *Manager) RunSandboxTask(ctx context.Context, instance, sandboxID string, argv []string, timeout time.Duration, maxOutput int64) (SandboxTaskResult, error) {
	if len(argv) == 0 || timeout <= 0 || maxOutput <= 0 {
		return SandboxTaskResult{}, errors.New("fcvm: incomplete sandbox task")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout+sandboxTemplateStartSlack)
	defer cancel()
	conn, err := m.DialSandboxSession(ctx, instance, sandboxID)
	if err != nil {
		return SandboxTaskResult{}, fmt.Errorf("fcvm: sandbox %s task: %w", instance, err)
	}
	defer func() { _ = conn.Close() }()
	var out SandboxTaskResult
	collect := func(stream uint32, data []byte) error {
		if stream != sandboxproto.FrameStdout {
			return nil
		}
		room := maxOutput - int64(len(out.Output))
		if int64(len(data)) > room {
			data, out.Truncated = data[:max(room, 0)], true
		}
		out.Output = append(out.Output, data...)
		return nil
	}
	var res sandboxproto.ExecResult
	op := sandboxproto.Request{Op: sandboxproto.OpExec, Argv: argv, TimeoutMillis: timeout.Milliseconds(), MaxOutputBytes: maxOutput}
	if err := sandboxproto.Do(ctx, conn, op, collect, &res); err != nil {
		return SandboxTaskResult{}, fmt.Errorf("fcvm: sandbox %s task: %w", instance, err)
	}
	out.ExitCode, out.TimedOut = res.ExitCode, res.TimedOut
	out.Truncated = out.Truncated || res.OutputTruncated
	return out, nil
}
