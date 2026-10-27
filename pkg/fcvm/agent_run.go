package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	// ErrAgentRunNotConfigured means this vmmd's VMM cannot dial an agent
	// run guest.
	ErrAgentRunNotConfigured = errors.New("fcvm: agent runs are not configured")
	// ErrAgentRunInstanceNotFound is returned before any run payload is sent
	// when the instance is not a live agent run VM on this node.
	ErrAgentRunInstanceNotFound = errors.New("fcvm: agent run instance is not live")
)

const (
	// agentRunDialWindow covers a cold boot of the agent base: Wake returns
	// once Firecracker is up, before guest-init listens.
	agentRunDialWindow = 30 * time.Second
	// agentRunExchangeSlack bounds the exchange past the run's own timeout:
	// the stop grace, output collection and the result frame.
	agentRunExchangeSlack  = time.Duration(api.AgentRunStopGraceSec)*2*time.Second + 30*time.Second
	agentRunDestroyTimeout = 10 * time.Second
)

// AgentRunWakeRequest is the machine for one agent run (ADR-20261027090010 §6): the
// agent-base drive0, the platform agent layer (drive1) that holds
// /etc/faas/agent.json, and the size admission chose. Command, prompt and
// inputs are deliberately absent; they cross later, in RunAgent.
type AgentRunWakeRequest struct {
	Instance      string
	AccountID     string
	Plan          api.Plan
	BaseKey       string
	LayerKey      string
	VcpuCount     int
	MemSizeMiB    int
	CPUMillicores int
	// Relay binds the VM's outbound relay to the run; nil leaves the relay
	// closed.
	Relay *AgentRunRelay
	// InputKey is the storage key of the run's input drive, the checkout
	// image agentd prepared (ADR-20261027090010 §3); empty: the drive holds zeros.
	InputKey string
	// Snapshot is the preset snapshot to restore, from
	// CaptureAgentRunSnapshot; nil, unusable or failing: a cold boot.
	Snapshot *Snapshot
}

// AgentRunRelay is the agent run principal of a run VM's relay (ADR-20261027090010
// §7, ADR-20261027090016). It is host-only: vmmd mints the relay assertion from it,
// and it never reaches guest-visible state.
type AgentRunRelay struct {
	RunID        string
	LeaseToken   string
	TenantID     string
	AgentID      string
	AgentVersion int
}

// Valid reports whether every identifier is well formed.
func (r AgentRunRelay) Valid() bool {
	for _, id := range []string{r.RunID, r.LeaseToken, r.AgentID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	if r.TenantID != "" {
		if _, err := uuid.Parse(r.TenantID); err != nil {
			return false
		}
	}
	return r.AgentVersion >= 1
}

// WakeAgentRun cold-boots one disposable agent run VM. It uses the
// execution-only path: a fresh jail, cgroup and lease, no tenant network,
// and no app readiness probe. The run's model calls use the vsock relay
// (rollout step 7), never a TAP.
func (m *Manager) WakeAgentRun(ctx context.Context, req AgentRunWakeRequest) (*Instance, error) {
	if m == nil || m.vmm == nil {
		return nil, ErrAgentRunNotConfigured
	}
	if req.Instance == "" || req.AccountID == "" || !req.Plan.Valid() || req.BaseKey == "" || req.LayerKey == "" ||
		req.VcpuCount <= 0 || req.MemSizeMiB <= 0 || req.CPUMillicores <= 0 {
		return nil, errors.New("fcvm: incomplete agent run wake request")
	}
	if req.Snapshot != nil && !req.Snapshot.Networkless {
		return nil, errors.New("fcvm: an agent run restores only a networkless snapshot")
	}
	var relay *AgentRunRelay
	if req.Relay != nil {
		if !req.Relay.Valid() {
			return nil, errors.New("fcvm: agent run relay identity is malformed")
		}
		copied := *req.Relay
		relay = &copied
	}
	inst, err := m.Wake(ctx, WakeRequest{
		Instance: req.Instance, ExecutionOnly: true, agentRunOnly: true, agentRunRelay: relay, agentRunInputKey: req.InputKey,
		AccountID: req.AccountID, Plan: req.Plan,
		BaseKey: req.BaseKey, LayerKey: req.LayerKey, VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
		CPUMillicores: req.CPUMillicores, Snapshot: req.Snapshot,
	})
	if err != nil {
		return nil, err
	}
	if inst == nil || !inst.ExecutionOnly || !inst.AgentRunOnly || !inst.Lease.Networkless {
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentRunDestroyTimeout)
		defer cancel()
		return nil, errors.Join(errors.New("fcvm: agent run wake did not produce a networkless agent VM"), m.Destroy(destroyCtx, req.Instance))
	}
	return inst, nil
}

// AgentRunDialer is implemented by JailerVMM.
type AgentRunDialer interface {
	DialAgentRun(context.Context, Lease) (net.Conn, error)
}

// RunAgent sends one run to a VM WakeAgentRun booted, streams its output to
// receive, and destroys the VM on every path, so a run never outlives its
// result (a parked app consumes zero resources; a finished run, too).
// Closing cancel asks the guest to stop the harness.
func (m *Manager) RunAgent(ctx context.Context, instance string, req agentrunproto.Request, receive agentrunproto.OutputReceiver, cancel <-chan struct{}) (agentrunproto.Result, error) {
	if err := req.Validate(); err != nil {
		return agentrunproto.Result{}, err
	}
	if m == nil || m.vmm == nil {
		return agentrunproto.Result{}, ErrAgentRunNotConfigured
	}
	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok || inst == nil || !inst.AgentRunOnly {
		return agentrunproto.Result{}, ErrAgentRunInstanceNotFound
	}
	defer func() {
		destroyCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), agentRunDestroyTimeout)
		defer stop()
		_ = m.Destroy(destroyCtx, instance)
	}()
	dialer, ok := m.vmm.(AgentRunDialer)
	if !ok {
		return agentrunproto.Result{}, ErrAgentRunNotConfigured
	}
	runCtx, stop := context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second+agentRunExchangeSlack)
	defer stop()
	conn, err := dialer.DialAgentRun(runCtx, inst.Lease)
	if err != nil {
		return agentrunproto.Result{}, fmt.Errorf("fcvm: dial agent run: %w", err)
	}
	defer func() { _ = conn.Close() }()
	result, err := agentrunproto.Run(runCtx, conn, req, receive, cancel)
	if err != nil {
		return agentrunproto.Result{}, fmt.Errorf("fcvm: agent run exchange: %w", err)
	}
	return result, nil
}

// DialAgentRun connects to the agent run listener of one live lease through
// the Firecracker vsock proxy. It never boots or restores a VM, so it cannot
// send a run's payload before the boot fence.
func (v *JailerVMM) DialAgentRun(ctx context.Context, l Lease) (net.Conn, error) {
	if v == nil || l.Instance == "" || v.chrootBase == "" || v.fcName == "" {
		return nil, errors.New("fcvm: dial agent run: vmm or lease is not configured")
	}
	deadline := time.Now().Add(agentRunDialWindow)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	lastErr := error(context.DeadlineExceeded)
	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: executionDialTimeout}).DialContext(ctx, "unix", v.vsockUDSSock(l.Instance))
		if err == nil {
			_ = conn.SetDeadline(minTime(deadline, time.Now().Add(executionDialTimeout)))
			if err = writeExecutionConnect(conn, agentrunproto.VsockPort); err == nil {
				var ack string
				if ack, err = readConnectAck(conn); err == nil && ack == "OK" {
					_ = conn.SetDeadline(time.Time{})
					return conn, nil
				} else if err == nil {
					err = fmt.Errorf("CONNECT rejected: %q", ack)
				}
			}
			_ = conn.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(executionDialStep):
		}
	}
	return nil, fmt.Errorf("fcvm: dial agent run vsock %s: %w", v.vsockUDSSock(l.Instance), lastErr)
}

// AgentRunRelayIdentity returns the account and the run principal bound to
// an agent run VM's relay. A run without one gets no relay.
func (m *Manager) AgentRunRelayIdentity(instance string) (accountID string, relay AgentRunRelay, err error) {
	if m == nil {
		return "", AgentRunRelay{}, fmt.Errorf("fcvm: agent run relay identity %s: not authorized", instance)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	if !ok || inst == nil || !inst.AgentRunOnly || inst.AgentRunRelay == nil {
		return "", AgentRunRelay{}, fmt.Errorf("fcvm: agent run relay identity %s: not authorized", instance)
	}
	return inst.AccountID, *inst.AgentRunRelay, nil
}
