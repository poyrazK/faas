package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sync/semaphore"
)

// ErrStaticEgressUnprovisioned revokes local enforcement while retaining the
// customer's pin intent for a later operator reprovisioning.
var ErrStaticEgressUnprovisioned = errors.New("static egress IP is not provisioned on this node")

type staticEgressWakeState struct {
	gate       *semaphore.Weighted
	generation uint64 // protected by gate
	accountID  string
}

// WithStaticEgressPolicySource installs the read-only durable app policy source.
// Configure it before serving requests. Notifications accelerate convergence;
// every production wake still checks the current intent before booting.
func (m *Manager) WithStaticEgressPolicySource(source func(context.Context, string, string) (*netip.Addr, error)) *Manager {
	m.staticEgressPolicySource = source
	return m
}

func sameStaticEgressIP(a, b *netip.Addr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (m *Manager) ensureStaticEgressAlias(ctx context.Context, accountID string, ip netip.Addr) error {
	m.staticEgressAliasesMu.Lock()
	defer m.staticEgressAliasesMu.Unlock()
	if m.staticEgressAliasesManaged && (!m.hasStaticEgressGrantLocked(accountID, ip)) {
		return ErrStaticEgressUnprovisioned
	}
	if err := m.run.Run(ctx, []string{"ip", "addr", "replace", ip.String() + "/32", "dev", "br-tenants", "label", staticEgressAliasLabel}); err != nil {
		return fmt.Errorf("install static egress alias %s: %w", ip, err)
	}
	if m.staticEgressAliasIPs == nil {
		m.staticEgressAliasIPs = make(map[netip.Addr]bool)
	}
	m.staticEgressAliasIPs[ip] = true
	return nil
}

func (m *Manager) prepareStaticEgressWake(ctx context.Context, req WakeRequest, qualificationOnly bool) error {
	if req.StaticEgressExcluded && req.StaticEgressIP != "" {
		return errors.New("excluded deployment cannot request static egress")
	}
	if req.StaticEgressExcluded || qualificationOnly || req.Quarantine || req.ExecutionOnly {
		return nil
	}
	generation := m.staticEgressWakeSequence.Add(1)
	var requested *netip.Addr
	if req.StaticEgressIP != "" {
		ip, err := netip.ParseAddr(req.StaticEgressIP)
		if err != nil {
			return fmt.Errorf("static egress IP: invalid dotted-quad %q: %w", req.StaticEgressIP, err)
		}
		if !ip.Is4() {
			return fmt.Errorf("static egress IP: rejected %q (IPv6 deferred)", req.StaticEgressIP)
		}
		if err := api.ValidateStaticEgressIP(ip); err != nil {
			return fmt.Errorf("static egress IP: rejected %q: %w", req.StaticEgressIP, err)
		}
		requested = &ip
	}
	authorized := false
	if m.staticEgressPolicySource != nil {
		current, err := m.staticEgressPolicySource(ctx, req.AccountID, req.AppID)
		if err != nil {
			return fmt.Errorf("read static egress intent: %w", err)
		}
		if !sameStaticEgressIP(current, requested) {
			return errors.New("static egress policy changed; retry with current app intent")
		}
		authorized = true
	} else {
		if requested != nil {
			m.staticEgressAliasesMu.Lock()
			authorized = m.staticEgressAliases[req.AppID] == *requested && m.staticEgressAliasAccounts[req.AppID] == req.AccountID
			m.staticEgressAliasesMu.Unlock()
		}
	}
	value, _ := m.staticEgressWakeStates.LoadOrStore(req.AppID, &staticEgressWakeState{gate: semaphore.NewWeighted(1)})
	state := value.(*staticEgressWakeState)
	if err := state.gate.Acquire(ctx, 1); err != nil {
		return err
	}
	defer state.gate.Release(1)
	m.perAppStaticIPMu.RLock()
	cached := m.perAppStaticIP[req.AppID]
	same := sameStaticEgressIP(cached, requested)
	m.perAppStaticIPMu.RUnlock()
	if generation < state.generation {
		if same && state.accountID == req.AccountID {
			if requested != nil {
				return m.ensureStaticEgressAlias(ctx, req.AccountID, *requested)
			}
			return nil
		}
		return errors.New("static egress policy changed; retry with current app intent")
	}
	if m.staticEgressPolicySource == nil && requested == nil && cached != nil {
		return errors.New("static egress policy changed; retry with current app intent")
	}
	if authorized {
		m.rememberStaticEgressAccount(req.AccountID, req.AppID)
	}
	if requested == nil {
		if authorized {
			if err := ReleaseStaticEgressIP(req.AccountID, req.AppID); err != nil {
				return err
			}
		}
	} else if authorized {
		if _, err := AcquireStaticEgressIP(req.AccountID, req.AppID, *requested); err != nil {
			return err
		}
	} else {
		res, ok := StaticEgressReservationFor(req.AppID)
		if !ok || res.AccountID != req.AccountID || res.CustomerIP != *requested {
			return errors.New("static egress IP has no matching authorized reservation")
		}
	}
	if requested != nil {
		if err := m.ensureStaticEgressAlias(ctx, req.AccountID, *requested); err != nil {
			return err
		}
	}
	m.perAppStaticIPMu.Lock()
	if m.perAppStaticIP == nil {
		m.perAppStaticIP = make(map[string]*netip.Addr)
	}
	m.perAppStaticIP[req.AppID] = requested
	delete(m.perAppStaticBlocked, req.AppID)
	if m.perAppStaticAccounts == nil {
		m.perAppStaticAccounts = make(map[string]string)
	}
	m.perAppStaticAccounts[req.AppID] = req.AccountID
	m.perAppStaticIPMu.Unlock()
	state.generation = generation
	state.accountID = req.AccountID
	return nil
}

// ReconcileStaticEgressPolicies repairs missed notifications, including clears
// while an app is parked. Update rechecks intent under the app mutation gate.
func (m *Manager) ReconcileStaticEgressPolicies(ctx context.Context) error {
	if m.staticEgressPolicySource == nil {
		return nil
	}
	m.perAppStaticIPMu.RLock()
	accounts := make(map[string]string, len(m.perAppStaticAccounts))
	for appID, accountID := range m.perAppStaticAccounts {
		accounts[appID] = accountID
	}
	m.perAppStaticIPMu.RUnlock()
	var errs []error
	for appID, accountID := range accounts {
		err := m.reconcileStaticEgressPolicy(ctx, accountID, appID)
		if err != nil {
			errs = append(errs, fmt.Errorf("app %s: %w", appID, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) reconcileStaticEgressPolicy(ctx context.Context, accountID, appID string) error {
	unlock, err := m.lockAppEgressPolicy(ctx, appID)
	if err != nil {
		return err
	}
	defer unlock()
	// Enforce the authoritative local removal before a database read can
	// consume the repair deadline. Keep reading intent afterward so a new,
	// locally provisioned pin can recover even if the cached pin was revoked.
	if cached := m.locallyRevokedStaticEgressIP(accountID, appID); cached != nil {
		if err := m.applyStaticEgressIPLocked(ctx, accountID, appID, cached, true); err != nil {
			return err
		}
	}
	ip, err := m.staticEgressPolicySource(ctx, accountID, appID)
	revoked := errors.Is(err, ErrStaticEgressUnprovisioned)
	if err != nil && !revoked {
		return err
	}
	if err := m.applyStaticEgressIPLocked(ctx, accountID, appID, ip, revoked); err != nil {
		if !errors.Is(err, ErrStaticEgressUnprovisioned) {
			return err
		}
		// A local bundle revocation can precede the database projection.
		if err := m.applyStaticEgressIPLocked(ctx, accountID, appID, ip, true); err != nil {
			return err
		}
		revoked = true
	}
	if ip == nil && !revoked {
		m.forgetClearedStaticEgressAccount(appID)
	}
	return nil
}

func (m *Manager) locallyRevokedStaticEgressIP(accountID, appID string) *netip.Addr {
	m.perAppStaticIPMu.RLock()
	cached := m.perAppStaticIP[appID]
	m.perAppStaticIPMu.RUnlock()
	if cached == nil {
		return nil
	}
	m.staticEgressAliasesMu.Lock()
	revoked := m.staticEgressAliasesManaged && !m.hasStaticEgressGrantLocked(accountID, *cached)
	m.staticEgressAliasesMu.Unlock()
	if !revoked {
		return nil
	}
	ip := *cached
	return &ip
}

func (m *Manager) rememberStaticEgressAccount(accountID, appID string) {
	m.perAppStaticIPMu.Lock()
	defer m.perAppStaticIPMu.Unlock()
	if m.perAppStaticAccounts == nil {
		m.perAppStaticAccounts = make(map[string]string)
	}
	m.perAppStaticAccounts[appID] = accountID
}

// Retire a parked clear only after the host projection succeeds. A failed
// clear stays in the repair roster even though desired intent is already nil.
// The caller holds the exclusive app gate, so no wake can publish meanwhile.
func (m *Manager) forgetClearedStaticEgressAccount(appID string) {
	m.mu.Lock()
	live := false
	for _, inst := range m.live {
		if inst.AppID == appID && !inst.StaticEgressExcluded && !inst.QualificationOnly && !inst.ExecutionOnly && !inst.Net.Quarantine {
			live = true
			break
		}
	}
	m.mu.Unlock()
	if !live {
		m.perAppStaticIPMu.Lock()
		delete(m.perAppStaticAccounts, appID)
		m.perAppStaticIPMu.Unlock()
	}
}

// The operator file carries an app label, but the durable provisioning gate
// grants an account/IP pair. Moving a valid IP between apps in that account
// must not require an operator file edit.
func (m *Manager) hasStaticEgressGrantLocked(accountID string, ip netip.Addr) bool {
	for appID, candidate := range m.staticEgressAliases {
		if candidate == ip && m.staticEgressAliasAccounts[appID] == accountID {
			return true
		}
	}
	return false
}
