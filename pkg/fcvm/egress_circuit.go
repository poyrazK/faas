package fcvm

// ADR-201 / ADR-375: schedd owns desired circuits; vmmd applies them to every
// live or pending tenant network before guest execution.

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/netns"
)

var ErrEgressCircuitDisabled = errors.New("fcvm: egress circuit enforcement is disabled on this node")
var ErrEgressCircuitRevision = errors.New("fcvm: invalid egress circuit revision")

type egressCircuitNetwork struct {
	appID           string
	net             netns.Config
	appliedRevision int64
	targetCount     int
	inSync          bool
}

type EgressCircuitStatus struct {
	Enabled         bool
	Applied         bool
	DesiredRevision int64
	AppliedRevision int64
	TargetCount     int
}

// EgressCircuitStatus reports completed nft enforcement, separately from
// probe health. It exposes no addresses, ports or plaintext hostnames.
func (m *Manager) EgressCircuitStatus(instance string) (EgressCircuitStatus, bool) {
	m.mu.Lock()
	inst, exists := m.live[instance]
	appID := ""
	if exists {
		appID = inst.AppID
	}
	m.mu.Unlock()
	if !exists {
		return EgressCircuitStatus{}, false
	}
	m.egressCircuitMu.Lock()
	defer m.egressCircuitMu.Unlock()
	network, registered := m.egressCircuitNetworks[instance]
	desired := m.appEgressCircuitRevisions[appID]
	return EgressCircuitStatus{
		Enabled:         m.egressCircuitEnabled && appID != "",
		Applied:         registered && network.inSync && network.appliedRevision == desired,
		DesiredRevision: desired, AppliedRevision: network.appliedRevision,
		TargetCount: network.targetCount,
	}, true
}

// WithEgressCircuitBreaker is daemon startup configuration. Call before
// enabling prepared networks or serving VM lifecycle/update RPCs.
func (m *Manager) WithEgressCircuitBreaker(enabled bool) *Manager {
	m.egressCircuitEnabled = enabled
	return m
}

// WithEgressCircuitSource configures the schedd-owned durable read source.
// Production requires this when enabled. Read failures refuse new guest boots.
func (m *Manager) WithEgressCircuitSource(source func(context.Context, string) (netns.EgressCircuitSnapshot, error)) *Manager {
	m.egressCircuitSource = source
	return m
}

// UpdateEgressCircuit retains the complete desired set even when the app is
// parked. Each namespace replacement is one atomic nft transaction. A failed
// application retains desired state for retry and is never acknowledged as
// successful enforcement. Pending wakes participate before they become live.
func (m *Manager) UpdateEgressCircuit(ctx context.Context, appID string, targets []netns.EgressCircuitTarget) error {
	_, err := m.UpdateEgressCircuitRevision(ctx, appID, netns.EgressCircuitSnapshot{Targets: targets})
	return err
}

func (m *Manager) UpdateEgressCircuitRevision(ctx context.Context, appID string, snapshot netns.EgressCircuitSnapshot) (int64, error) {
	if appID == "" {
		return 0, fmt.Errorf("fcvm: UpdateEgressCircuit: empty app_id")
	}
	if !m.egressCircuitEnabled {
		return 0, ErrEgressCircuitDisabled
	}
	if snapshot.Revision < 0 || (m.egressCircuitSource != nil && snapshot.Revision == 0) {
		return 0, ErrEgressCircuitRevision
	}
	targets, err := canonicalEgressCircuitTargets(snapshot.Targets)
	if err != nil {
		return 0, err
	}
	m.egressCircuitMu.Lock()
	defer m.egressCircuitMu.Unlock()
	if snapshot.Revision > 0 {
		prior := m.appEgressCircuitRevisions[appID]
		if snapshot.Revision < prior {
			// Re-apply the newer remembered set: it may still be pending after
			// an earlier partial failure. A stale RPC must never regress it.
			targets = m.appEgressCircuits[appID]
			snapshot.Revision = prior
		} else if snapshot.Revision == prior && !slices.Equal(targets, m.appEgressCircuits[appID]) {
			return prior, ErrEgressCircuitRevision
		}
	}
	m.rememberEgressCircuitSnapshotLocked(appID, netns.EgressCircuitSnapshot{Revision: snapshot.Revision, Targets: targets})
	var failures []error
	for id, network := range m.egressCircuitNetworks {
		if network.appID != appID {
			continue
		}
		if err := m.applyEgressCircuits(ctx, network.net, targets); err != nil {
			network.inSync = false
			m.egressCircuitNetworks[id] = network
			failures = append(failures, fmt.Errorf("fcvm: UpdateEgressCircuit instance=%s: %w", id, err))
		} else {
			network.appliedRevision = snapshot.Revision
			network.targetCount = len(targets)
			network.inSync = true
			m.egressCircuitNetworks[id] = network
		}
	}
	return snapshot.Revision, errors.Join(failures...)
}

func (m *Manager) rememberEgressCircuitSnapshotLocked(appID string, snapshot netns.EgressCircuitSnapshot) {
	if snapshot.Revision > 0 {
		if m.appEgressCircuitRevisions == nil {
			m.appEgressCircuitRevisions = make(map[string]int64)
		}
		m.appEgressCircuitRevisions[appID] = snapshot.Revision
	}
	m.rememberEgressCircuitsLocked(appID, snapshot.Targets)
}

func (m *Manager) rememberEgressCircuitsLocked(appID string, targets []netns.EgressCircuitTarget) {
	if len(targets) == 0 {
		delete(m.appEgressCircuits, appID)
		return
	}
	if m.appEgressCircuits == nil {
		m.appEgressCircuits = make(map[string][]netns.EgressCircuitTarget)
	}
	m.appEgressCircuits[appID] = targets
}

// Register after network setup and before boot/restore. Holding the same lock
// as Update makes a simultaneous change either seed this network or patch it
// as a pending network; it cannot fall between those two paths.
func (m *Manager) registerEgressCircuitNetwork(ctx context.Context, appID string, nc netns.Config) error {
	if !nc.EgressCircuitEnabled {
		return nil
	}
	var durable netns.EgressCircuitSnapshot
	if m.egressCircuitSource != nil {
		var err error
		durable, err = m.egressCircuitSource(ctx, appID)
		if err != nil {
			return fmt.Errorf("fcvm: load egress circuits before guest boot: %w", err)
		}
		durable.Targets, err = canonicalEgressCircuitTargets(durable.Targets)
		if err != nil || durable.Revision < 0 || (durable.Revision == 0 && len(durable.Targets) > 0) {
			return fmt.Errorf("fcvm: invalid durable circuit policy: %w", errors.Join(err, ErrEgressCircuitRevision))
		}
	}
	m.egressCircuitMu.Lock()
	defer m.egressCircuitMu.Unlock()
	if m.egressCircuitSource != nil && durable.Revision >= m.appEgressCircuitRevisions[appID] {
		m.rememberEgressCircuitSnapshotLocked(appID, durable)
	}
	if targets := m.appEgressCircuits[appID]; len(targets) > 0 {
		if err := m.applyEgressCircuits(ctx, nc, targets); err != nil {
			return fmt.Errorf("fcvm: seed egress circuit before guest boot: %w", err)
		}
	}
	if m.egressCircuitNetworks == nil {
		m.egressCircuitNetworks = make(map[string]egressCircuitNetwork)
	}
	m.egressCircuitNetworks[nc.Instance] = egressCircuitNetwork{appID: appID, net: nc,
		appliedRevision: m.appEgressCircuitRevisions[appID], targetCount: len(m.appEgressCircuits[appID]), inSync: true}
	return nil
}

// Call after killing the guest and before deleting the namespace. It also
// waits for any in-progress patch, avoiding updates to a reused namespace.
func (m *Manager) unregisterEgressCircuitNetwork(instance string) {
	m.egressCircuitMu.Lock()
	defer m.egressCircuitMu.Unlock()
	delete(m.egressCircuitNetworks, instance)
}

func (m *Manager) applyEgressCircuits(ctx context.Context, nc netns.Config, targets []netns.EgressCircuitTarget) error {
	if !nc.EgressCircuitEnabled {
		return ErrEgressCircuitDisabled
	}
	return m.runNftCommands(ctx, nc.Netns, nc.EgressCircuitSetCommands(targets))
}

func canonicalEgressCircuitTargets(in []netns.EgressCircuitTarget) ([]netns.EgressCircuitTarget, error) {
	return netns.CanonicalEgressCircuitTargets(in)
}
