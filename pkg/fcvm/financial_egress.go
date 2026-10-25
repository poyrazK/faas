// adr: 1748 — polling selects a refill; the kernel quota is the authority.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/netns"
)

func (m *Manager) financialEgressLow(local *localComputeLease) (bool, error) {
	local.mu.Lock()
	defer local.mu.Unlock()
	if !local.lease.EgressFunded {
		return false, nil
	}
	if local.egressBroken {
		return false, financial.ErrComputeUnavailable
	}
	m.mu.Lock()
	inst := m.live[local.lease.InstanceID]
	var peer string
	if inst != nil && inst.Net.VethPeer != "" {
		peer = inst.Net.VethHost
	}
	m.mu.Unlock()
	if peer == "" {
		return false, nil
	} // boot still owns the original quota
	observed, err := m.readFinancialEgress(peer)
	if err != nil || observed < local.egressObserved {
		local.egressBroken = true
		return false, errors.Join(financial.ErrComputeUnavailable, err)
	}
	// This is only a low-water hint. Counter gaps and high packet rates can
	// stall at the kernel boundary, but cannot spend beyond a financed quota.
	// The mark is the netdev share, so namespace-local ARP cannot exhaust its
	// gate before a refill is asked for.
	mark, _ := netns.EgressBudgetQuotas(local.lease.EgressGrant)
	return observed-local.egressObserved >= uint64(mark), nil
}

// Caller holds local.mu through the DB response and exactly one helper call.
func (m *Manager) syncFinancialEgress(ctx context.Context, local *localComputeLease, l financial.ComputeLease, refill bool) bool {
	if local.egressBroken || !l.EgressFunded || (refill && (l.EgressSequence != local.egressApplied+1 || l.EgressGrant < api.FinancialEgressMinimumCreditBytes || l.EgressGrant > api.FinancialEgressCreditBytes)) {
		local.egressBroken = true
		return false
	}
	m.mu.Lock()
	inst := m.live[l.InstanceID]
	var nc netns.Config
	var generation string
	if inst != nil {
		nc = inst.Net
		generation = inst.nativeGeneration
	}
	m.mu.Unlock()
	if inst == nil || nc.VethPeer == "" {
		return true
	} // booting or networkless execution
	if nc.EgressBudgetBytes <= 0 {
		local.egressBroken = true
		return false
	}
	observed, err := m.readFinancialEgress(nc.VethHost)
	if err != nil || observed < local.egressObserved {
		local.egressBroken = true
		return false
	}
	scoped, err := m.nativeInstanceNetworkContext(ctx, l.InstanceID, generation)
	if err != nil {
		local.egressBroken = true
		return false
	}
	// Consume the local sequence before issuing an effect. Any uncertainty is
	// fail-closed; no retry is allowed to reset this funded anonymous quota.
	nc.EgressBudgetUntil = l.RunUntil.Add(-2 * api.FinancialComputeAuthorityTimeout)
	cmds := nc.EgressBudgetDeadlineCommands()
	if refill {
		local.egressApplied = l.EgressSequence
		cmds = append(cmds, nc.EgressBudgetRefillCommands(l.EgressGrant)...)
	}
	if err = m.runNftCommands(scoped, nc.Netns, cmds); err != nil {
		local.egressBroken = true
		m.log.Warn("financial egress credit installation pending retirement", "instance", l.InstanceID, "error", fmt.Errorf("install byte credit: %w", err))
		return false
	}
	if refill {
		local.egressObserved = observed
	}
	return true
}

func (m *Manager) readFinancialEgress(peer string) (uint64, error) {
	if m.financialEgressRead != nil {
		return m.financialEgressRead(peer)
	}
	return netns.ReadVethRXBytes(peer)
}
func financialEgressClockFits(l financial.ComputeLease, started time.Time) bool {
	// GrantedAt is sampled after request start; network latency is bounded by
	// the same timeout. The early kernel expiry covers this allowed skew.
	return !l.GrantedAt.Before(started.Add(-2*api.FinancialComputeAuthorityTimeout)) && !l.GrantedAt.After(time.Now().Add(api.FinancialComputeAuthorityTimeout))
}
