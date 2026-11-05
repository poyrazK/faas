package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrResolvedEgressNoInstance is returned when no live instance owns the
// resolver query's source address.
var ErrResolvedEgressNoInstance = errors.New("fcvm: no live instance for the resolver source address")

// databaseEgressKey is one address . port pair of a bound managed database
// (ADR 20261026150000).
type databaseEgressKey struct {
	addr netip.Addr
	port uint16
}

// clampResolvedTTL bounds a DNS answer's TTL for DNS-gated egress (ADR-373).
func clampResolvedTTL(ttl time.Duration) time.Duration {
	lo := time.Duration(api.DNSGatedEgressMinTTLSeconds) * time.Second
	hi := time.Duration(api.DNSGatedEgressMaxTTLSeconds) * time.Second
	return min(max(ttl, lo), hi)
}

// AllowResolvedEgress lets the captured owner of source open TCP to addrs for
// the answer's ttl, clamped (ADR-373). Its network generation must still own
// that source when the grant arrives. The bridge
// resolver calls it before returning an answer, so the guest never sees an
// address it cannot reach yet. Addresses already allowed for more than half
// the TTL are skipped, so repeated lookups of a name cost no nft call.
//
// A non-zero databasePort means the name is one of the app's bound managed
// databases (ADR 20261026150000): the addresses are also allowed on that
// port, outside the plan's egress ports, in the same nft transaction.
func (m *Manager) AllowResolvedEgress(ctx context.Context, source netip.Addr, owner ServiceCallerIdentity, addrs []netip.Addr, ttl time.Duration, databasePort uint16) error {
	// Cleanup records its intent before joining this lock in circuit unregister.
	// Finish an admitted grant before namespace teardown can recycle its name.
	m.egressCircuitMu.Lock()
	defer m.egressCircuitMu.Unlock()
	ttl = clampResolvedTTL(ttl)
	now := time.Now()
	source = source.Unmap()
	m.mu.Lock()
	id := owner.InstanceID
	inst := m.live[id]
	if inst == nil || m.pendingCleanup[id] != nil || inst.Net.HostIP != source || owner.NetworkGeneration == "" || inst.resolvedEgressGeneration != owner.NetworkGeneration {
		m.mu.Unlock()
		return ErrResolvedEgressNoInstance
	}
	m.mu.Unlock()
	unlock := m.lockInstanceNetworkPolicy(inst)
	defer unlock()
	m.mu.Lock()
	if m.live[id] != inst || m.pendingCleanup[id] != nil || inst.Net.HostIP != source || inst.resolvedEgressGeneration != owner.NetworkGeneration {
		m.mu.Unlock()
		return ErrResolvedEgressNoInstance
	}
	gated := inst.Net.DNSGated && inst.Net.Netns != ""
	database := databasePort != 0 && inst.Net.DatabaseEgress && inst.Net.Netns != ""
	if !gated && !database {
		m.mu.Unlock()
		return nil
	}
	var add []netip.Addr
	var addDatabase []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if !a.IsValid() {
			continue
		}
		if until, ok := inst.resolvedEgress[a]; gated && (!ok || until.Sub(now) <= ttl/2) {
			add = append(add, a)
		}
		if until, ok := inst.databaseEgress[databaseEgressKey{a, databasePort}]; database && (!ok || until.Sub(now) <= ttl/2) {
			addDatabase = append(addDatabase, a)
		}
	}
	nc, appID := inst.Net, inst.AppID
	m.mu.Unlock()
	if len(add) == 0 && len(addDatabase) == 0 {
		return nil
	}
	ctx, err := m.nativeInstanceNetworkContext(ctx, id, inst.nativeGeneration)
	if err != nil {
		return err
	}
	cmds := nc.ResolvedEgressAddCommands(add, ttl)
	cmds = append(cmds, nc.DatabaseEgressAddCommands(addDatabase, databasePort, ttl)...)
	if err := m.runNftCommands(ctx, nc.Netns, cmds); err != nil {
		return fmt.Errorf("fcvm: allow resolved egress for %s: %w", id, err)
	}
	until := now.Add(ttl)
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.live[id]; ok && cur == inst {
		if inst.resolvedEgress == nil {
			inst.resolvedEgress = make(map[netip.Addr]time.Time, len(add))
		}
		for _, a := range add {
			inst.resolvedEgress[a] = until
		}
		if inst.databaseEgress == nil && len(addDatabase) > 0 {
			inst.databaseEgress = make(map[databaseEgressKey]time.Time, len(addDatabase))
		}
		for _, a := range addDatabase {
			inst.databaseEgress[databaseEgressKey{a, databasePort}] = until
		}
	}
	m.rememberAppResolvedLocked(appID, add, until, now)
	m.rememberAppDatabaseLocked(appID, addDatabase, databasePort, until, now)
	return nil
}

// rememberAppResolvedLocked records resolved addresses for seeding the app's
// next instances, pruning expired entries and capping the app's set.
func (m *Manager) rememberAppResolvedLocked(appID string, addrs []netip.Addr, until, now time.Time) {
	if appID == "" {
		return
	}
	if m.appResolved == nil {
		m.appResolved = make(map[string]map[netip.Addr]time.Time)
	}
	seen := m.appResolved[appID]
	if seen == nil {
		seen = make(map[netip.Addr]time.Time)
		m.appResolved[appID] = seen
	}
	for a, exp := range seen {
		if !exp.After(now) {
			delete(seen, a)
		}
	}
	for _, a := range addrs {
		if _, ok := seen[a]; !ok && len(seen) >= api.DNSGatedEgressAppSeedMax {
			continue
		}
		seen[a] = until
	}
}

// rememberAppDatabaseLocked records bound database pairs for seeding the
// app's next instances, like rememberAppResolvedLocked.
func (m *Manager) rememberAppDatabaseLocked(appID string, addrs []netip.Addr, port uint16, until, now time.Time) {
	if appID == "" || len(addrs) == 0 {
		return
	}
	if m.appDatabaseResolved == nil {
		m.appDatabaseResolved = make(map[string]map[databaseEgressKey]time.Time)
	}
	seen := m.appDatabaseResolved[appID]
	if seen == nil {
		seen = make(map[databaseEgressKey]time.Time)
		m.appDatabaseResolved[appID] = seen
	}
	for k, exp := range seen {
		if !exp.After(now) {
			delete(seen, k)
		}
	}
	for _, a := range addrs {
		key := databaseEgressKey{a, port}
		if _, ok := seen[key]; !ok && len(seen) >= api.DNSGatedEgressAppSeedMax {
			continue
		}
		seen[key] = until
	}
}

// seedResolvedEgress adds the app's recently resolved addresses to a new
// instance's egress_resolved set, and its bound databases' pairs to its
// egress_db set. It is best effort: a guest whose first connection races the
// seed retries its SYN, and a fresh lookup through the resolver allows the
// address anyway.
func (m *Manager) seedResolvedEgress(ctx context.Context, instance string) {
	m.egressCircuitMu.Lock()
	defer m.egressCircuitMu.Unlock()
	m.mu.Lock()
	inst, ok := m.live[instance]
	if !ok || inst.AppID == "" || inst.Net.Netns == "" || !inst.Net.DNSGated && !inst.Net.DatabaseEgress {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	unlock := m.lockInstanceNetworkPolicy(inst)
	defer unlock()
	m.mu.Lock()
	if m.live[instance] != inst || m.pendingCleanup[instance] != nil {
		m.mu.Unlock()
		return
	}
	now := time.Now()
	addrs := map[netip.Addr]time.Time{}
	if inst.Net.DNSGated {
		for a, until := range m.appResolved[inst.AppID] {
			if until.Sub(now) >= time.Second {
				addrs[a] = until
			}
		}
	}
	pairs := map[databaseEgressKey]time.Time{}
	if inst.Net.DatabaseEgress {
		for k, until := range m.appDatabaseResolved[inst.AppID] {
			if until.Sub(now) >= time.Second {
				pairs[k] = until
			}
		}
	}
	nc := inst.Net
	m.mu.Unlock()
	if len(addrs) == 0 && len(pairs) == 0 {
		return
	}
	ctx, err := m.nativeInstanceNetworkContext(ctx, instance, inst.nativeGeneration)
	if err != nil {
		if m.log != nil {
			m.log.Warn("fcvm: seed resolved egress ownership failed", "instance", instance, "err", err)
		}
		return
	}
	var cmds [][]string
	for address, until := range addrs {
		remaining := time.Until(until)
		if remaining < time.Second {
			delete(addrs, address)
			continue
		}
		cmds = append(cmds, nc.ResolvedEgressAddCommands([]netip.Addr{address}, remaining)...)
	}
	for pair, until := range pairs {
		remaining := time.Until(until)
		if remaining < time.Second {
			delete(pairs, pair)
			continue
		}
		cmds = append(cmds, nc.DatabaseEgressAddCommands([]netip.Addr{pair.addr}, pair.port, remaining)...)
	}
	if err := m.runNftCommands(ctx, nc.Netns, cmds); err != nil {
		if m.log != nil {
			m.log.Warn("fcvm: seed resolved egress failed", "instance", instance, "addresses", len(addrs), "databases", len(pairs), "err", err)
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.live[instance]; ok && cur == inst {
		if inst.resolvedEgress == nil {
			inst.resolvedEgress = make(map[netip.Addr]time.Time, len(addrs))
		}
		for a, until := range addrs {
			inst.resolvedEgress[a] = until
		}
		if inst.databaseEgress == nil && len(pairs) > 0 {
			inst.databaseEgress = make(map[databaseEgressKey]time.Time)
		}
		for pair, until := range pairs {
			inst.databaseEgress[pair] = until
		}
	}
}
