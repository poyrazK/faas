package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
)

// restoreAdoptedStaticEgress rebuilds an adopted guest's static egress
// (ADR-20261128090000) before the guest is registered: the app's pin and
// account, the address reservation and the bridge alias. Adoption's host
// render that follows then emits the guest's SNAT from its surviving lease,
// before any other render, so its traffic never leaves through shared
// egress. A recorded block is restored as a block (still fail-closed). The
// periodic reconcile re-reads the durable intent and the operator grants
// afterwards and corrects either. A wake of the same app since startup has
// already set the pin and wins.
func (m *Manager) restoreAdoptedStaticEgress(ctx context.Context, accountID, appID string, a *resourceAdoption) error {
	n := a.Network
	if n.StaticEgressExcluded || (n.StaticEgressIP == "" && !n.StaticEgressBlocked) {
		return nil
	}
	if appID == "" || accountID == "" {
		return errors.New("static egress record lacks its app or account")
	}
	var pin *netip.Addr
	if n.StaticEgressIP != "" {
		ip, err := netip.ParseAddr(n.StaticEgressIP)
		if err != nil || !ip.Is4() {
			return fmt.Errorf("static egress address %q", n.StaticEgressIP)
		}
		pin = &ip
	}
	m.perAppStaticIPMu.RLock()
	_, woken := m.perAppStaticIP[appID]
	m.perAppStaticIPMu.RUnlock()
	if woken {
		return nil
	}
	if pin != nil && !n.StaticEgressBlocked {
		// The alias is kernel state that normally outlives vmmd; restore it
		// in case it did not. Grants are not loaded yet, so this bypasses
		// the grant check the reconcile applies later.
		if err := m.run.Run(ctx, []string{"ip", "addr", "replace", pin.String() + "/32", "dev", "br-tenants", "label", staticEgressAliasLabel}); err != nil {
			return fmt.Errorf("restore static egress alias %s: %w", pin, err)
		}
		m.staticEgressAliasesMu.Lock()
		if m.staticEgressAliasIPs == nil {
			m.staticEgressAliasIPs = make(map[netip.Addr]bool)
		}
		m.staticEgressAliasIPs[*pin] = true
		m.staticEgressAliasesMu.Unlock()
		if _, err := AcquireStaticEgressIP(accountID, appID, *pin); err != nil {
			return fmt.Errorf("restore static egress reservation: %w", err)
		}
	}
	m.perAppStaticIPMu.Lock()
	defer m.perAppStaticIPMu.Unlock()
	if m.perAppStaticIP == nil {
		m.perAppStaticIP = make(map[string]*netip.Addr)
	}
	m.perAppStaticIP[appID] = pin
	if n.StaticEgressBlocked {
		if m.perAppStaticBlocked == nil {
			m.perAppStaticBlocked = make(map[string]bool)
		}
		m.perAppStaticBlocked[appID] = true
	}
	if m.perAppStaticAccounts == nil {
		m.perAppStaticAccounts = make(map[string]string)
	}
	m.perAppStaticAccounts[appID] = accountID
	return nil
}
