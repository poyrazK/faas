// adr: 20261112121456 §2 — a web-mode sandbox session's VM sends DNS only to
// the public-names resolver, reaches none of the bridge's service ports or
// platform addresses, and opens new TCP only on its egress ports to addresses
// that resolver answered.
package netns

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestSandboxWebNetworkReachesOnlyThePublicResolver(t *testing.T) {
	c := NewConfigWithBridge("sandbox-sb", "fc-sb", "vh0", "vp0", netip.MustParseAddr("10.100.0.9"), netip.MustParseAddr("10.100.0.1"))
	c.SandboxWeb, c.DNSGated, c.EgressPorts = true, true, []uint16{80, 443}
	c.ServiceProxyHTTPS = true
	c.ServiceAddressCIDR = netip.MustParsePrefix("198.19.0.0/16")
	c.DatabaseRouteAddress = netip.MustParseAddr("198.18.0.53")
	lines := renderedLines(c)
	all := strings.Join(lines, "\n")
	var pin []string
	for _, argv := range c.GuestDNSPinCommands(func(parts ...string) []string { return parts }) {
		pin = append(pin, strings.Join(argv, " "))
	}
	if !strings.Contains(strings.Join(pin, "\n"), "dnat to 10.100.0.1:"+strconv.Itoa(SandboxWebDNSPort)) {
		t.Error("sandbox DNS is not pinned to the public-names resolver")
	}
	bridge := "ip daddr 10.100.0.1 "
	for _, want := range []string{
		bridge + "udp dport " + strconv.Itoa(SandboxWebDNSPort) + " accept",
		bridge + "tcp dport " + strconv.Itoa(SandboxWebDNSPort) + " accept",
		"daddr != @" + EgressResolvedSet,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing rule %q", want)
		}
	}
	for _, port := range []int{ServiceDiscoveryDNSPort, SandboxRegistriesDNSPort, ServiceProxyPort, LegacyServiceProxyPort, ServiceProxyHTTPSPort} {
		for _, proto := range []string{"tcp", "udp"} {
			if frag := bridge + proto + " dport " + strconv.Itoa(port) + " accept"; strings.Contains(all, frag) {
				t.Errorf("sandbox reaches bridge service port: %q", frag)
			}
		}
	}
	for _, block := range []string{c.ServiceAddressCIDR.Masked().String(), c.DatabaseRouteAddress.String()} {
		lineIndex(t, lines, "ip daddr "+block+" drop")
		for _, line := range lines {
			if strings.Contains(line, "ip daddr "+block+" ") && strings.HasSuffix(line, "accept") {
				t.Errorf("sandbox reaches a platform address: %q", line)
			}
		}
	}
}
