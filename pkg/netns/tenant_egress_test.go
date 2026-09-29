// adr: 361 — tenant egress is default-deny: web ports, pinned DNS, per-VM
// rate limits.
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func egressTestConfig() Config {
	c := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	c.EgressPorts = []uint16{443, 80, 443, 0, 5432}
	c.EgressConnRate, c.EgressConnBurst = 10, 40
	return c
}

func renderedLines(c Config) []string {
	var out []string
	for _, argv := range c.NftCommands() {
		out = append(out, strings.Join(argv, " "))
	}
	return out
}

// lineIndex returns the index of the first line containing every fragment.
func lineIndex(t *testing.T, lines []string, fragments ...string) int {
	t.Helper()
	for i, line := range lines {
		ok := true
		for _, f := range fragments {
			if !strings.Contains(line, f) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	t.Fatalf("no rendered line contains %q:\n%s", fragments, strings.Join(lines, "\n"))
	return -1
}

func TestTenantEgressPolicyObjectsAndDNSPin(t *testing.T) {
	lines := renderedLines(egressTestConfig())
	for _, family := range []string{"ip", "ip6"} {
		lineIndex(t, lines, "add set "+family+" faas egress_ports { type inet_service ; }")
		lineIndex(t, lines, "add element "+family+" faas egress_ports { 80,443,5432 }")
		lineIndex(t, lines, "add counter "+family+" faas faas_egress_denied")
		lineIndex(t, lines, "add counter "+family+" faas faas_egress_rate")
	}
	lineIndex(t, lines, "add chain ip faas guest_dns { type nat hook prerouting priority dstnat ; }")
	lineIndex(t, lines, "add rule ip faas guest_dns iifname tap0 udp dport 53 dnat to 10.100.0.1:53")
	lineIndex(t, lines, "add rule ip faas guest_dns iifname tap0 tcp dport 53 dnat to 10.100.0.1:53")
}

// Guest-originated traffic meets, in order: bridge services (incl. pinned
// DNS), the lateral-movement deny, the rate limit, the non-TCP drop, the
// ADR-031 allowlist accept, the SMTP drop, the port policy and the allowlist
// terminal drop. Pinned DNS must be accepted before the 10/8 deny that
// covers the bridge address.
func TestTenantEgressRuleOrder(t *testing.T) {
	c := egressTestConfig()
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	lines := renderedLines(c)
	v4 := []int{
		lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr 10.100.0.1 udp dport 53 accept"),
		lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr 10.0.0.0/8"),
		lineIndex(t, lines, "rule ip faas forward iifname tap0 ct state new limit rate over 10/second burst 40 packets counter name faas_egress_rate drop"),
		lineIndex(t, lines, "rule ip faas forward iifname tap0 meta l4proto != tcp counter name faas_egress_denied drop"),
		lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 }"),
		lineIndex(t, lines, "rule ip faas forward iifname tap0 tcp dport {", "} drop"),
		lineIndex(t, lines, "rule ip faas forward iifname tap0 tcp dport != @egress_ports counter name faas_egress_denied drop"),
		lineIndex(t, lines, "rule ip faas forward iifname tap0 counter name deny_allowlist drop"),
	}
	for i := 1; i < len(v4); i++ {
		if v4[i-1] >= v4[i] {
			t.Fatalf("v4 rule %d (line %d) is not before rule %d (line %d):\n%s", i-1, v4[i-1], i, v4[i], strings.Join(lines, "\n"))
		}
	}
	v6 := []int{
		lineIndex(t, lines, "rule ip6 faas forward iifname tap0 ct state new limit rate over 10/second"),
		lineIndex(t, lines, "rule ip6 faas forward iifname tap0 meta l4proto != tcp"),
		lineIndex(t, lines, "rule ip6 faas forward iifname tap0 tcp dport != @egress_ports"),
	}
	for i := 1; i < len(v6); i++ {
		if v6[i-1] >= v6[i] {
			t.Fatalf("v6 policy rules out of order: %v", v6)
		}
	}
	// Ingress is untouched: every new policy rule is scoped to the guest.
	for _, line := range lines {
		if strings.Contains(line, " rule ") && (strings.Contains(line, "faas_egress_") || strings.Contains(line, "@egress_ports")) &&
			!strings.Contains(line, "iifname tap0") {
			t.Fatalf("egress policy rule is not scoped to the guest tap: %s", line)
		}
	}
}

// A caller that forgets EgressPorts gets an empty set and the port rule
// still renders, so all guest TCP drops (fail closed). A zero rate renders
// no rate rule.
func TestTenantEgressFailsClosedWithoutPorts(t *testing.T) {
	c := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	lines := renderedLines(c)
	lineIndex(t, lines, "add set ip faas egress_ports { type inet_service ; }")
	lineIndex(t, lines, "rule ip faas forward iifname tap0 tcp dport != @egress_ports")
	for _, line := range lines {
		if strings.Contains(line, "add element ip faas egress_ports") || strings.Contains(line, "faas_egress_rate") {
			t.Fatalf("unexpected line for a config without ports or rate: %s", line)
		}
	}
}

func TestEgressPortElements(t *testing.T) {
	for _, tc := range []struct {
		ports []uint16
		want  string
	}{
		{nil, ""},
		{[]uint16{0}, ""},
		{[]uint16{443, 80, 443}, "80,443"},
		{[]uint16{8883, 443, 5432, 80}, "80,443,5432,8883"},
	} {
		if got := (Config{EgressPorts: tc.ports}).EgressPortElements(); got != tc.want {
			t.Errorf("EgressPortElements(%v) = %q, want %q", tc.ports, got, tc.want)
		}
	}
}

// The app-port retarget flushes prerouting; guest DNS pinning lives in its
// own chain so the retarget can never remove it.
func TestRetargetAppPortLeavesGuestDNSPin(t *testing.T) {
	c := egressTestConfig()
	c.GuestAppPort = 3000
	for _, argv := range c.RetargetAppPortCommands() {
		if strings.Contains(strings.Join(argv, " "), guestDNSChain) {
			t.Fatalf("retarget touches the guest DNS chain: %v", argv)
		}
	}
}
