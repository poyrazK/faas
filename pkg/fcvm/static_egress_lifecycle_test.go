package fcvm

// adr: 20261104001001

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestStaticEgressDurableIntentRestoresAndRejectsStaleWake(t *testing.T) {
	const account, app = "acct-static-durable", "app-static-durable"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	ip := netip.MustParseAddr("203.0.113.76")
	var current *netip.Addr = &ip
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithStaticEgressPolicySource(func(_ context.Context, accountID, appID string) (*netip.Addr, error) {
		if accountID != account || appID != app {
			return nil, errors.New("wrong app owner")
		}
		return current, nil
	})
	req := WakeRequest{AccountID: account, AppID: app, StaticEgressIP: ip.String()}
	if err := m.prepareStaticEgressWake(t.Context(), req, false); err != nil {
		t.Fatal(err)
	}
	if res, ok := StaticEgressReservationFor(app); !ok || res.CustomerIP != ip {
		t.Fatal("fresh node did not restore the authorized reservation")
	}
	req.StaticEgressIP = ""
	if err := m.prepareStaticEgressWake(t.Context(), req, false); err == nil {
		t.Fatal("stale unpinned dispatch was accepted")
	}
	current = nil
	req.StaticEgressIP = ip.String()
	if err := m.prepareStaticEgressWake(t.Context(), req, false); err == nil {
		t.Fatal("stale pinned dispatch recreated a cleared reservation")
	}
	req.StaticEgressIP = ""
	if err := m.prepareStaticEgressWake(t.Context(), req, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := StaticEgressReservationFor(app); ok {
		t.Fatal("cleared durable intent retained its reservation")
	}
}

func TestStaticEgressMissedNotificationReconciliation(t *testing.T) {
	prior := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(prior) })
	const account, app = "acct-static-repair", "app-static-repair"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	ip := netip.MustParseAddr("203.0.113.77")
	var current *netip.Addr = &ip
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithStaticEgressPolicySource(func(context.Context, string, string) (*netip.Addr, error) { return current, nil })
	m.SetHostRenderer(&fakeHostRenderer{})
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, ip.String()); err != nil {
		t.Fatal(err)
	}
	seedPolicyInstance(m, "repair-live", app, api.PlanScale)
	current = nil
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, ip.String()); err == nil {
		t.Fatal("stale notification replaced durable clear")
	}
	if err := m.ReconcileStaticEgressPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.perAppStaticIP[app] != nil {
		t.Fatal("missed clear was not repaired")
	}
	if _, ok := StaticEgressReservationFor(app); ok {
		t.Fatal("missed clear retained reservation")
	}
	current = &ip
	if err := m.ReconcileStaticEgressPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.perAppStaticIP[app] == nil || *m.perAppStaticIP[app] != ip {
		t.Fatal("missed live pin was not repaired")
	}
}

func TestStaticEgressAliasFailureBlocksReservationAcknowledgement(t *testing.T) {
	m := newTestManager(&fakeRunner{failOn: "ip addr replace"}, &fakeVMM{})
	if err := m.UpdateStaticEgressIP(t.Context(), "acct-static-fail", "app-static-fail", "203.0.113.78"); err == nil {
		t.Fatal("failed alias acknowledged")
	}
}

func TestStaticEgressParkedClearRetriesFailedHostProjection(t *testing.T) {
	previous := *netns.ActiveHostPolicyForRender()
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	const account, app = "acct-static-retry-clear", "app-static-retry-clear"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	ip := netip.MustParseAddr("203.0.113.79")
	var current *netip.Addr = &ip
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithStaticEgressPolicySource(func(context.Context, string, string) (*netip.Addr, error) { return current, nil })
	renderer := &fakeHostRenderer{}
	m.SetHostRenderer(renderer)
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, ip.String()); err != nil {
		t.Fatal(err)
	}
	current = nil
	renderer.renderErr = errors.New("host reload failed")
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, ""); err == nil {
		t.Fatal("failed host clear acknowledged")
	}
	renderer.renderErr = nil
	before := renderer.renderCalls
	if err := m.ReconcileStaticEgressPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if renderer.renderCalls <= before {
		t.Fatal("parked clear disappeared from retry roster")
	}
	if _, ok := m.perAppStaticAccounts[app]; ok {
		t.Fatal("converged parked clear stayed in the database polling roster")
	}
}

func TestStaticEgressOperatorRevocationClearsEnforcementAndCanRecover(t *testing.T) {
	previous := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(previous) })
	const account, app = "acct-operator-revoke", "app-operator-revoke"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	ip := netip.MustParseAddr("203.0.113.97")
	revoked := false
	var sourceErr error
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithStaticEgressPolicySource(func(context.Context, string, string) (*netip.Addr, error) {
		if sourceErr != nil {
			if len(netns.ActiveHostPolicyForRender().StaticEgressBlockedSources) != 1 {
				t.Fatal("database read preceded local revocation enforcement")
			}
			return nil, sourceErr
		}
		if revoked {
			return nil, ErrStaticEgressUnprovisioned
		}
		return &ip, nil
	})
	m.SetHostRenderer(&fakeHostRenderer{})
	entry := []StaticEgressIPEntry{{AccountID: account, AppID: "operator-label", IP: ip}}
	m.SetStaticEgressIPAliases(entry)
	m.SetStaticEgressIPAliases([]StaticEgressIPEntry{{AccountID: account, AppID: "renamed-operator-label", IP: ip}})
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, ip.String()); err != nil {
		t.Fatal(err)
	}
	seedPolicyInstance(m, "operator-revoke-live", app, api.PlanScale)
	m.live["operator-revoke-live"].Lease.HostIP = netip.MustParseAddr("10.100.0.77")
	if err := m.ReconcileStaticEgressPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressRules) != 1 {
		t.Fatal("fixture did not install live SNAT")
	}
	m.SetStaticEgressIPAliases(nil)
	sourceErr = errors.New("policy database unavailable")
	if err := m.ReconcileStaticEgressPolicies(t.Context()); !errors.Is(err, sourceErr) {
		t.Fatalf("database failure was hidden: %v", err)
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressRules) != 0 || len(netns.ActiveHostPolicyForRender().StaticEgressBlockedSources) != 1 {
		t.Fatal("database outage postponed local static egress revocation")
	}
	sourceErr = nil
	// Even before the database publication completes, a local revocation
	// must prevent the repair path from reinstalling the removed alias.
	if err := m.ReconcileStaticEgressPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !m.perAppStaticBlocked[app] {
		t.Fatal("local revocation was undone by the durable old pin")
	}
	revoked = true
	if err := m.ReconcileStaticEgressPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressRules) != 0 {
		t.Fatal("revoked provisioning retained SNAT")
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressBlockedSources) != 1 {
		t.Fatal("revoked pin fell back to shared public egress")
	}
	if _, ok := StaticEgressReservationFor(app); ok {
		t.Fatal("revoked provisioning retained reservation")
	}
	m.SetStaticEgressIPAliases(entry)
	revoked = false
	if err := m.ReconcileStaticEgressPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressRules) != 1 {
		t.Fatal("reprovisioned pin did not recover")
	}
	if len(netns.ActiveHostPolicyForRender().StaticEgressBlockedSources) != 0 {
		t.Fatal("reprovisioned pin retained the block")
	}
}

func TestBlockedStaticEgressRetainsAdoptionRefusal(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.perAppStaticBlocked = map[string]bool{"blocked-app": true}
	block := &resourceAdoption{}
	m.adoptionPolicyFor("blocked-instance", "blocked-app", block)
	reason, _ := adoptionScope(nil, false, resourceJournalRecord{Adoption: block})
	if reason != adoptionRefusedStaticEgress {
		t.Fatalf("blocked static egress was eligible for unpinned adoption: %s", reason)
	}
}
