package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

func (m *Manager) lockInstanceNetworkPolicy(inst *Instance) func() {
	m.mu.Lock()
	if inst.networkPolicyMu == nil {
		inst.networkPolicyMu = new(sync.Mutex)
	}
	mu := inst.networkPolicyMu
	m.mu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// The attachment prefix owns the member address. Peer destinations may sort
// before it, but must never supply the interface mask or firewall baseline.
func memberFirstPrivateNetworkCIDRs(cidrs []netip.Prefix, address netip.Addr) ([]netip.Prefix, error) {
	for i, prefix := range cidrs {
		if prefix.Contains(address) {
			out := make([]netip.Prefix, 0, len(cidrs))
			out = append(out, prefix)
			out = append(out, cidrs[:i]...)
			return append(out, cidrs[i+1:]...), nil
		}
	}
	return nil, fmt.Errorf("member address %s is outside the private network", address)
}

// replaceNetworkPolicy runs while the app policy gate is held. DNS
// updates use the instance lock; circuit updates use egressCircuitMu. Restore
// their live elements in the replacement transaction, retaining their TTLs.
func (m *Manager) replaceNetworkPolicy(ctx context.Context, inst *Instance, nc netns.Config) error {
	m.egressCircuitMu.Lock()
	defer m.egressCircuitMu.Unlock()
	unlock := m.lockInstanceNetworkPolicy(inst)
	defer unlock()
	now := time.Now()
	commands := append(nc.NftResetCommands(), nc.NftCommands()...)
	commands = append(commands, nc.EgressCircuitSetCommands(m.appEgressCircuits[inst.AppID])...)
	m.mu.Lock()
	if m.live[inst.Lease.Instance] != inst {
		m.mu.Unlock()
		return errors.New("fcvm: private network original live targets changed")
	}
	commands = append(commands, nc.PrivateNetworkHistoryCommands(inst.Net.PrivateNetworkCIDRs)...)
	if nc.DNSGated {
		for addr, until := range inst.resolvedEgress {
			if until.Sub(now) >= time.Second {
				commands = append(commands, nc.ResolvedEgressAddCommands([]netip.Addr{addr}, until.Sub(now))...)
			}
		}
	}
	if nc.DatabaseEgress {
		for key, until := range inst.databaseEgress {
			if until.Sub(now) >= time.Second {
				commands = append(commands, nc.DatabaseEgressAddCommands([]netip.Addr{key.addr}, key.port, until.Sub(now))...)
			}
		}
	}
	m.mu.Unlock()
	err := m.runAtomicNetworkPolicy(ctx, nc.Netns, commands)
	// A replacement may commit before an error is returned. Never reuse
	// handles from the old tables, including on an uncertain acknowledgement.
	m.mu.Lock()
	inst.AllowlistHandleV4, inst.AllowlistHandleV6 = 0, 0
	inst.PrivateNetworkHandleV4, inst.PrivateNetworkHandleV6 = 0, 0
	m.markAdoptionDirty(inst.Lease.Instance)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	h4, h6, err := m.captureAllowlistHandlesForWake(ctx, nc.Netns, nc.EgressAllowlist)
	if err != nil {
		return err
	}
	m.mu.Lock()
	inst.AllowlistHandleV4, inst.AllowlistHandleV6 = h4, h6
	m.markAdoptionDirty(inst.Lease.Instance)
	m.mu.Unlock()
	return nil
}

// nft parses a command string as one transaction even when a runner cannot
// supply stdin. Never fall back to individual delete/add processes on live VMs.
func (m *Manager) runAtomicNetworkPolicy(ctx context.Context, namespace string, commands [][]string) error {
	runner, err := m.networkCommandRunner(ctx)
	if err != nil {
		return err
	}
	return runAtomicNftPolicy(ctx, runner, namespace, commands)
}

func runAtomicNftPolicy(ctx context.Context, runner Runner, namespace string, commands [][]string) error {
	if len(commands) == 0 {
		return nil
	}
	prefix := []string{"ip", "netns", "exec", namespace, "nft"}
	parts := make([]string, 0, len(commands))
	for _, argv := range commands {
		if len(argv) <= len(prefix) || !slices.Equal(argv[:len(prefix)], prefix) {
			return fmt.Errorf("unexpected nft replacement prefix: %v", argv)
		}
		parts = append(parts, strings.Join(argv[len(prefix):], " "))
	}
	script := strings.Join(parts, "\n") + "\n"
	if inputRunner, ok := runner.(InputRunner); ok {
		if err := inputRunner.RunInput(ctx, append(prefix, "-f", "-"), []byte(script)); err != nil {
			return fmt.Errorf("nft ruleset batch: %w", err)
		}
		return nil
	}
	return runner.Run(ctx, append(prefix, script))
}
