package fcvm

// Egress circuit application (ADR-201 §3).
//
// vmmd is the only component that touches a network namespace, so schedd's
// breaker reaches the data plane through here. The operation is deliberately
// tiny compared with UpdateEgressAllowlist: circuits are nftables SET
// ELEMENTS, not rules, so there are no handles to track and no per-family
// revert — flushing and re-adding converges from any prior state.

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/netns"
)

// UpdateEgressCircuit makes the open-circuit set of every live instance of
// appID exactly `targets`.
//
// Whole-set semantics, mirroring UpdateEgressAllowlist: the caller pushes the
// complete desired state rather than a delta. That is what makes this
// self-healing — after a vmmd restart the re-rendered netns carries an empty
// set while schedd still believes circuits are open, and the next reconcile
// simply re-installs them instead of failing on a delete of something that
// was never there.
//
// Idempotent, and a no-op when the app has no live instances: a parked app
// has no netns to hold a rule, and the wake path renders the set fresh.
func (m *Manager) UpdateEgressCircuit(ctx context.Context, appID string, targets []netns.EgressCircuitTarget) error {
	if appID == "" {
		return fmt.Errorf("fcvm: UpdateEgressCircuit: empty app_id")
	}
	// Snapshot the live netns configs under the manager lock, then release it
	// before any netns exec — the same discipline UpdateEgressAllowlist uses,
	// because an nft exec is far too slow to hold the manager lock across.
	type target struct {
		instanceID string
		net        netns.Config
	}
	var live []target
	m.mu.Lock()
	for id, inst := range m.live {
		if inst.AppID != appID {
			continue
		}
		live = append(live, target{instanceID: id, net: inst.Net})
	}
	m.mu.Unlock()
	if len(live) == 0 {
		return nil
	}

	for _, t := range live {
		nc := t.net
		// The feature flag lives on the Config the instance was woken with,
		// so an operator who enabled the flag after a wake does not get a
		// half-configured netns: the set, counter and rule are declared at
		// wake time or not at all. A flag flip takes effect on the next wake.
		cmds := nc.EgressCircuitSetCommands(targets)
		if len(cmds) == 0 {
			continue
		}
		if err := m.runCommands(ctx, cmds); err != nil {
			return fmt.Errorf("fcvm: UpdateEgressCircuit app=%s netns=%s: %w", appID, nc.Netns, err)
		}
	}
	return nil
}
