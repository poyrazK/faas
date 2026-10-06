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

// clampResolvedTTL bounds a DNS answer's TTL for DNS-gated egress (ADR-373).
func clampResolvedTTL(ttl time.Duration) time.Duration {
	lo := time.Duration(api.DNSGatedEgressMinTTLSeconds) * time.Second
	hi := time.Duration(api.DNSGatedEgressMaxTTLSeconds) * time.Second
	return min(max(ttl, lo), hi)
}

// AllowResolvedEgress lets the instance whose host-side address is source
// open TCP to addrs for the answer's ttl, clamped (ADR-373). The bridge
// resolver calls it before returning an answer, so the guest never sees an
// address it cannot reach yet. Addresses already allowed for more than half
// the TTL are skipped, so repeated lookups of a name cost no nft call.
func (m *Manager) AllowResolvedEgress(ctx context.Context, source netip.Addr, addrs []netip.Addr, ttl time.Duration) error {
	ttl = clampResolvedTTL(ttl)
	now := time.Now()
	source = source.Unmap()
	m.mu.Lock()
	var id string
	var inst *Instance
	for k, v := range m.live {
		if v.Net.HostIP == source {
			id, inst = k, v
			break
		}
	}
	if inst == nil {
		m.mu.Unlock()
		return ErrResolvedEgressNoInstance
	}
	if !inst.Net.DNSGated || inst.Net.Netns == "" {
		m.mu.Unlock()
		return nil
	}
	var add []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if !a.IsValid() {
			continue
		}
		if until, ok := inst.resolvedEgress[a]; ok && until.Sub(now) > ttl/2 {
			continue
		}
		add = append(add, a)
	}
	nc, appID := inst.Net, inst.AppID
	m.mu.Unlock()
	if len(add) == 0 {
		return nil
	}
	ctx, err := m.nativeInstanceNetworkContext(ctx, id, inst.nativeGeneration)
	if err != nil {
		return err
	}
	if err := m.runNftCommands(ctx, nc.Netns, nc.ResolvedEgressAddCommands(add, ttl)); err != nil {
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
	}
	m.rememberAppResolvedLocked(appID, add, until, now)
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

// seedResolvedEgress adds the app's recently resolved addresses to a new
// instance's egress_resolved set. It is best effort: a guest whose first
// connection races the seed retries its SYN, and a fresh lookup through
// the resolver allows the address anyway.
func (m *Manager) seedResolvedEgress(ctx context.Context, instance string) {
	now := time.Now()
	m.mu.Lock()
	inst, ok := m.live[instance]
	if !ok || !inst.Net.DNSGated || inst.AppID == "" || inst.Net.Netns == "" {
		m.mu.Unlock()
		return
	}
	var addrs []netip.Addr
	for a, until := range m.appResolved[inst.AppID] {
		if until.After(now) {
			addrs = append(addrs, a)
		}
	}
	nc := inst.Net
	m.mu.Unlock()
	if len(addrs) == 0 {
		return
	}
	ctx, err := m.nativeInstanceNetworkContext(ctx, instance, inst.nativeGeneration)
	if err != nil {
		if m.log != nil {
			m.log.Warn("fcvm: seed resolved egress ownership failed", "instance", instance, "err", err)
		}
		return
	}
	ttl := time.Duration(api.DNSGatedEgressMinTTLSeconds) * time.Second
	if err := m.runNftCommands(ctx, nc.Netns, nc.ResolvedEgressAddCommands(addrs, ttl)); err != nil {
		if m.log != nil {
			m.log.Warn("fcvm: seed resolved egress failed", "instance", instance, "addresses", len(addrs), "err", err)
		}
		return
	}
	until := now.Add(ttl)
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.live[instance]; ok && cur == inst {
		if inst.resolvedEgress == nil {
			inst.resolvedEgress = make(map[netip.Addr]time.Time, len(addrs))
		}
		for _, a := range addrs {
			inst.resolvedEgress[a] = until
		}
	}
}
