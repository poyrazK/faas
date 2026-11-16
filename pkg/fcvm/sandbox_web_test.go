// adr: 20261112121456 §2–§4 — a web-mode sandbox session VM boots with a
// DNS-gated network, egress on 80 and 443 and its DNS pinned to the
// public-names resolver; resolved addresses open only for public names and
// only within the plan's distinct-domain budget; and it boots only through
// the tenant egress gateway, unless the operator allowed node egress.
package fcvm

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func webSandboxWake(instance string) SandboxWakeRequest {
	req := registriesSandboxWake(instance)
	req.Plan, req.Network = api.PlanHobby, api.SandboxNetworkWeb
	return req
}

// withTenantEgressGateway installs a host policy whose tenant egress leaves
// through iface for the test's duration.
func withTenantEgressGateway(t *testing.T, iface string) {
	t.Helper()
	old := *netns.ActiveHostPolicyForRender()
	next := old
	next.TenantEgressIface = iface
	netns.SwapActiveHostPolicy(next)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(old) })
}

func TestWebSandboxBootsDNSGatedOnWebPorts(t *testing.T) {
	withTenantEgressGateway(t, "wg-tenant")
	run := &fakeInputRunner{}
	m := newTestManager(run, &sandboxTestVMM{fakeVMM: &fakeVMM{}})
	inst, err := m.WakeSandbox(t.Context(), webSandboxWake("sbx-web"))
	if err != nil {
		t.Fatalf("WakeSandbox: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "sbx-web") })
	if inst.Lease.Networkless || !inst.Lease.AgentSized {
		t.Fatalf("lease = %+v; want a networked, agent-sized VM", inst.Lease)
	}
	nc := inst.Net
	if !nc.SandboxWeb || nc.SandboxRegistries || !nc.DNSGated || !slices.Equal(nc.EgressPorts, []uint16{80, 443}) || nc.DatabaseEgress {
		t.Fatalf("network = %+v; want web, DNS-gated, 80 and 443", nc)
	}
	if nc.GuestMAC != netns.SandboxGuestMAC {
		t.Fatalf("guest MAC %q; want the guarded %q", nc.GuestMAC, netns.SandboxGuestMAC)
	}
	pinned := false
	for _, argv := range nc.NftCommands() {
		pinned = pinned || strings.Contains(strings.Join(argv, " "), "dnat to "+nc.HostBridgeIP.String()+":"+itoa(netns.SandboxWebDNSPort))
	}
	if !pinned {
		t.Fatal("guest DNS is not pinned to the public-names resolver")
	}

	source := nc.HostIP
	owner := resolvedOwner(t, m, source)
	addr := []netip.Addr{netip.MustParseAddr("198.51.100.30")}
	before := run.inputRuns
	for _, name := range []string{"", "api.svc.gregale", "db.internal", "localhost"} {
		if err := m.AllowResolvedEgressNamed(t.Context(), source, owner, name, addr, time.Minute, 0); err != nil || run.inputRuns != before {
			t.Fatalf("%q opened egress (runs %d, err %v)", name, run.inputRuns-before, err)
		}
	}
	if err := m.AllowResolvedEgressNamed(t.Context(), source, owner, "www.example.com", addr, time.Minute, 0); err != nil || run.inputRuns != before+1 ||
		!strings.Contains(string(run.input), "egress_resolved { 198.51.100.30") {
		t.Fatalf("a public answer did not open egress (runs %d, err %v):\n%s", run.inputRuns-before, err, run.input)
	}
}

func TestWebSandboxDistinctDomainBudget(t *testing.T) {
	withTenantEgressGateway(t, "wg-tenant")
	run := &fakeInputRunner{}
	m := newTestManager(run, &sandboxTestVMM{fakeVMM: &fakeVMM{}})
	inst, err := m.WakeSandbox(t.Context(), webSandboxWake("sbx-budget"))
	if err != nil {
		t.Fatalf("WakeSandbox: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "sbx-budget") })
	source := inst.Net.HostIP
	owner := resolvedOwner(t, m, source)
	limit := api.SandboxWebDistinctDomains[api.PlanHobby.PlanIndex()]
	grant := func(name string, i int) error {
		a := netip.AddrFrom4([4]byte{198, 51, byte(100 + i/250), byte(1 + i%250)})
		return m.AllowResolvedEgressNamed(t.Context(), source, owner, name, []netip.Addr{a}, time.Minute, 0)
	}
	for i := range limit {
		if err := grant("www.site"+itoa(i)+".com", i); err != nil {
			t.Fatalf("domain %d of %d refused: %v", i+1, limit, err)
		}
	}
	// Another name of a granted domain shares its registrable domain.
	if err := grant("cdn.site0.com", limit); err != nil {
		t.Fatalf("a granted domain's subdomain was refused: %v", err)
	}
	err = grant("www.one-too-many.co.uk", limit+1)
	if ResolvedEgressRefusal(err) == "" || !strings.Contains(err.Error(), "distinct domain limit") {
		t.Fatalf("domain %d = %v; want the distinct-domain refusal", limit+1, err)
	}
}

func TestWebSandboxFailsClosedWithoutTheGateway(t *testing.T) {
	withTenantEgressGateway(t, "")
	m := newTestManager(&fakeInputRunner{}, &sandboxTestVMM{fakeVMM: &fakeVMM{}})
	if _, err := m.WakeSandbox(t.Context(), webSandboxWake("sbx-nogw")); err == nil || !strings.Contains(err.Error(), "tenant egress gateway") {
		t.Fatalf("WakeSandbox = %v, want a refusal naming the gateway", err)
	}
	m.mu.Lock()
	_, live := m.live["sbx-nogw"]
	m.mu.Unlock()
	if live {
		t.Fatal("a refused sandbox stayed live")
	}

	off := newTestManager(&fakeInputRunner{}, &sandboxTestVMM{fakeVMM: &fakeVMM{}}).WithDNSGatedEgress(false).WithSandboxWebNodeEgress(true)
	if _, err := off.WakeSandbox(t.Context(), webSandboxWake("sbx-nogate")); err == nil || !strings.Contains(err.Error(), "DNS-gated") {
		t.Fatalf("WakeSandbox without the DNS gate = %v, want a refusal naming it", err)
	}

	allowed := newTestManager(&fakeInputRunner{}, &sandboxTestVMM{fakeVMM: &fakeVMM{}}).WithSandboxWebNodeEgress(true)
	if _, err := allowed.WakeSandbox(t.Context(), webSandboxWake("sbx-node")); err != nil {
		t.Fatalf("a test fleet's node egress was refused: %v", err)
	}
	t.Cleanup(func() { _ = allowed.Destroy(context.Background(), "sbx-node") })
}

func TestSandboxWebDomainIsRegistrable(t *testing.T) {
	for name, want := range map[string]string{
		"www.example.com.": "example.com", "a.b.example.co.uk": "example.co.uk", "foo.github.io": "foo.github.io",
		"Example.COM": "example.com", "weird.unknowntld": "weird.unknowntld",
	} {
		if got := sandboxWebDomain(name); got != want {
			t.Errorf("sandboxWebDomain(%q) = %q, want %q", name, got, want)
		}
	}
}
