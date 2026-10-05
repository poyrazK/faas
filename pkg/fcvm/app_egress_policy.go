package fcvm

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sync/semaphore"
)

type appEgressPolicy struct {
	revision  int64
	allowlist []netip.Prefix
	ports     []uint16
}

// A per-app gate orders physical writes and wake publication without holding
// the manager's live-map mutex across network or Firecracker operations.
func (m *Manager) lockAppEgressPolicy(ctx context.Context, appID string) (func(), error) {
	return m.acquireAppEgressPolicy(ctx, appID, math.MaxInt64)
}

// Wakes share the gate so the app's ordinary concurrency is preserved. A
// writer waits for all in-flight wakes to publish before enumerating live VMs.
func (m *Manager) lockAppEgressPolicyForWake(ctx context.Context, appID string) (func(), error) {
	return m.acquireAppEgressPolicy(ctx, appID, 1)
}

func (m *Manager) acquireAppEgressPolicy(ctx context.Context, appID string, weight int64) (func(), error) {
	if appID == "" {
		return func() {}, nil
	}
	if ctx == nil { // Legacy cache-only callers predate context use.
		ctx = context.Background() //nolint:contextcheck // Legacy nil-context cache writes have no parent to inherit.
	}
	gate, _ := m.appEgressPolicyLocks.LoadOrStore(appID, semaphore.NewWeighted(math.MaxInt64))
	sem := gate.(*semaphore.Weighted)
	if err := sem.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	return func() { sem.Release(weight) }, nil
}

func (m *Manager) hasRevisionedAppEgressPolicy(appID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appEgressPolicies[appID].revision > 0
}

// Record accepted intent before physical writes: a partial failure must never
// let an older, weaker request replace it. The same revision can retry its
// complete projection; success is returned only after both controls apply.
func (m *Manager) UpdateAppEgressPolicy(ctx context.Context, appID string, revision int64, allowlist []netip.Prefix, ports []uint16) error {
	if appID == "" || revision <= 0 {
		return fmt.Errorf("fcvm: invalid app egress policy identity or revision")
	}
	policy, err := normalizeAppEgressPolicy(revision, allowlist, ports)
	if err != nil {
		return err
	}
	unlock, err := m.lockAppEgressPolicy(ctx, appID)
	if err != nil {
		return err
	}
	defer unlock()
	m.mu.Lock()
	current := m.appEgressPolicies[appID]
	if revision < current.revision || revision == current.revision && (!slices.Equal(current.allowlist, policy.allowlist) || !slices.Equal(current.ports, policy.ports)) {
		m.mu.Unlock()
		return fmt.Errorf("fcvm: stale or conflicting app egress policy revision")
	}
	if m.appEgressPolicies == nil {
		m.appEgressPolicies = map[string]appEgressPolicy{}
	}
	for _, inst := range m.live {
		if inst.AppID == appID {
			if err := validateAppEgressPolicyPlan(inst.Plan, policy); err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	m.appEgressPolicies[appID] = policy
	m.mu.Unlock()
	if err := m.updateEgressAllowlist(ctx, appID, policy.allowlist); err != nil {
		return err
	}
	return m.updateEgressPorts(ctx, appID, policy.ports)
}

func normalizeAppEgressPolicy(revision int64, allowlist []netip.Prefix, ports []uint16) (appEgressPolicy, error) {
	p := appEgressPolicy{revision: revision, allowlist: append([]netip.Prefix{}, allowlist...), ports: append([]uint16{}, ports...)}
	for i, prefix := range p.allowlist {
		if !prefix.IsValid() || prefix.Bits() == 0 {
			return p, fmt.Errorf("fcvm: invalid app egress policy CIDR")
		}
		p.allowlist[i] = prefix.Masked()
	}
	slices.SortFunc(p.allowlist, func(a, b netip.Prefix) int {
		if n := a.Addr().Compare(b.Addr()); n != 0 {
			return n
		}
		return a.Bits() - b.Bits()
	})
	p.allowlist = slices.Compact(p.allowlist)
	extras := p.ports[:0]
	for _, port := range p.ports {
		if _, forbidden := api.TenantEgressForbiddenPort(int(port)); port == 0 || forbidden {
			return p, fmt.Errorf("fcvm: forbidden app egress policy port")
		}
		if !slices.Contains(api.TenantEgressBasePorts(), port) {
			extras = append(extras, port)
		}
	}
	p.ports = extras
	slices.Sort(p.ports)
	p.ports = slices.Compact(p.ports)
	return p, nil
}

func validateAppEgressPolicyPlan(plan api.Plan, policy appEgressPolicy) error {
	if !plan.Valid() || len(policy.ports) > plan.EgressExtraPortsMax() {
		return fmt.Errorf("fcvm: app egress policy exceeds live workload plan")
	}
	return nil
}

func egressPrefixStrings(prefixes []netip.Prefix) []string {
	out := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		out[i] = prefix.String()
	}
	return out
}

func (m *Manager) reapplyAppEgressOperatorBundle(ctx context.Context, appID string) error {
	unlock, err := m.lockAppEgressPolicy(ctx, appID)
	if err != nil {
		return err
	}
	defer unlock()
	// Reread after taking the gate. A pre-lock cache snapshot could otherwise
	// undo a newer application projection while refreshing the operator bundle.
	return m.updateEgressAllowlist(ctx, appID, m.currentAppEgressAllowlist(appID))
}

func (m *Manager) currentAppEgressAllowlist(appID string) []netip.Prefix {
	m.perAppAllowlistMu.RLock()
	defer m.perAppAllowlistMu.RUnlock()
	return append([]netip.Prefix{}, m.perAppAllowlist[appID]...)
}
