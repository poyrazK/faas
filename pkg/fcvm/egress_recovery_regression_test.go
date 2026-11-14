// adr: 20261105103000
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// Model the acknowledged-error boundary: the nft transaction has committed,
// but its caller does not receive a successful acknowledgement.
type portCommitAckRunner struct {
	fakeRunner
	lastPorts  string
	portWrites int
	loseAck    bool
}

func (r *portCommitAckRunner) RunInput(_ context.Context, _ []string, input []byte) error {
	script := string(input)
	if strings.Contains(script, "flush set ip faas egress_ports") {
		r.lastPorts = script
		r.portWrites++
		if r.loseAck {
			r.loseAck = false
			return errors.New("lost acknowledgement after port-set commit")
		}
	}
	return nil
}

func TestEgressPortRevocationAfterUncertainExpansion(t *testing.T) {
	r := &portCommitAckRunner{loseAck: true}
	m := newTestManager(r, &fakeVMM{})
	seedPolicyInstance(m, "audit-port", "audit-app", api.PlanPro)
	if err := m.UpdateAppEgressPolicy(t.Context(), "audit-app", 1, nil, []uint16{5432}); err == nil {
		t.Fatal("fixture did not lose acknowledgement")
	}
	if !strings.Contains(r.lastPorts, "5432") {
		t.Fatal("fixture did not commit the extra port")
	}
	for _, revision := range []int64{2, 2} {
		if err := m.UpdateAppEgressPolicy(t.Context(), "audit-app", revision, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(r.lastPorts, "5432") {
		t.Fatalf("revocation and its retry returned success but port 5432 remains in the live set; physical port writes=%d", r.portWrites)
	}
}

func TestEmptyAllowlistAfterUncertainEnable(t *testing.T) {
	r := &allowlistTxnRunner{handles: map[string]map[string]uint64{}, next: 1000, loseAck: true}
	m := newTestManager(r, &fakeVMM{}).WithCaptureRunner(r)
	seedPolicyInstance(m, "audit-cidr", "audit-app", api.PlanPro)
	inst := m.live["audit-cidr"]
	r.handles[inst.Net.Netns] = map[string]uint64{"ip": 0, "ip6": 0}
	if err := m.UpdateAppEgressPolicy(t.Context(), "audit-app", 1, []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}, nil); err == nil {
		t.Fatal("fixture did not lose acknowledgement")
	}
	if r.handles[inst.Net.Netns]["ip"] == 0 {
		t.Fatal("fixture did not commit the restrictive allowlist")
	}
	for _, revision := range []int64{2, 2} {
		if err := m.UpdateAppEgressPolicy(t.Context(), "audit-app", revision, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if r.handles[inst.Net.Netns]["ip"] != 0 {
		t.Fatalf("empty policy and retry returned success but restrictive allowlist remains; physical transactions=%d", len(r.batches))
	}
}

func TestAdoptedGuestCanRenewResolvedEgress(t *testing.T) {
	f := newAdoptionFixture(t)
	m, vmm := adoptionManager(t, f, f.block())
	r := &fakeInputRunner{}
	m.run = r
	report, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil || report.Adopted != 1 {
		t.Fatalf("adoption=%+v err=%v", report, err)
	}
	inst := m.live[idLive]
	owner, matched := m.ResolveServiceCaller(inst.Lease.HostIP)
	if !matched || owner.NetworkGeneration == "" {
		t.Fatal("adopted guest has no owner")
	}
	before := r.inputRuns
	if err := m.AllowResolvedEgress(t.Context(), inst.Net.HostIP, owner, []netip.Addr{netip.MustParseAddr("203.0.113.8")}, time.Minute, 5432); err != nil {
		t.Fatal(err)
	}
	if r.inputRuns == before {
		t.Fatalf("fresh DNS grant acknowledged without any nft write: DNSGated=%v DatabaseEgress=%v", inst.Net.DNSGated, inst.Net.DatabaseEgress)
	}
}

func TestAdoptedPolicyRebuildRetainsEgressGuards(t *testing.T) {
	f := newAdoptionFixture(t)
	m, vmm := adoptionManager(t, f, f.block())
	r := &fakeInputRunner{}
	m.run = r
	report, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil || report.Adopted != 1 {
		t.Fatalf("adoption=%+v err=%v", report, err)
	}
	inst := m.live[idLive]
	if err := m.UpdateAppEgressPolicy(t.Context(), inst.AppID, 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), inst.AppID, 2, []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), inst.AppID, 3, nil, nil); err != nil {
		t.Fatal(err)
	}
	script := string(r.input)
	for _, guard := range []string{"ip daddr != @egress_resolved", "limit rate over", "@egress_db accept"} {
		if !strings.Contains(script, guard) {
			t.Errorf("policy rebuild of adopted guest removed egress guard %q", guard)
		}
	}
}

func TestPublicAllowlistRendererHandleRoundTrip(t *testing.T) {
	for _, family := range []string{"ip", "ip6"} {
		t.Run(family, func(t *testing.T) {
			nc := netns.NewConfig("audit-family", "fc-audit-family", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
			nc.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/64")}
			render := nc.ForwardAllowlistRule
			if family == "ip6" {
				render = nc.ForwardAllowlistRule6
			}
			rule := render(func(parts ...string) []string { return parts })
			output := strings.ReplaceAll(strings.Join(rule[5:], " "), "iifname tap0", `iifname "tap0"`) + " # handle 42\n"
			for _, listing := range []string{output, strings.ReplaceAll(output, ",", ", ")} {
				h, err := listChainHandles(t.Context(), &fakeCaptureRunner{listChainOutput: []byte(listing)}, nc.Netns, family, "faas", "forward")
				if err != nil || h != 42 {
					t.Fatalf("renderer handle=%d error=%v listing=%s", h, err, listing)
				}
			}
		})
	}
}

func TestPublicAllowlistHandleGuardIsExact(t *testing.T) {
	for _, tc := range []struct {
		name, guard string
		want        uint64
	}{
		{"legacy", "25", 42},
		{"current", "{ " + netns.AllowlistBlockedPortsSet() + " }", 42},
		{"incomplete set", "{ 22,25 }", 0},
		{"database port", "5432", 0},
		{"named set", "@egress_db", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := `iifname "tap0" ip daddr { 203.0.113.0/24 } tcp dport != ` + tc.guard + " accept # handle 42\n"
			h, err := listChainHandles(t.Context(), &fakeCaptureRunner{listChainOutput: []byte(line)}, "fc-owner", "ip", "faas", "forward")
			if err != nil || h != tc.want {
				t.Fatalf("handle=%d error=%v want=%d", h, err, tc.want)
			}
		})
	}
}
