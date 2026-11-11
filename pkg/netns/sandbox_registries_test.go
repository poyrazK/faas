// adr: 20261027090013 §6 — a registries-mode sandbox session's VM sends DNS
// only to the registries-only resolver, reaches none of the bridge's service
// ports, and opens new TCP only on 443 to addresses that resolver answered.
package netns

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestSandboxRegistriesNetworkReachesOnlyTheRegistriesResolver(t *testing.T) {
	c := NewConfigWithBridge("sandbox-sb", "fc-sb", "vh0", "vp0", netip.MustParseAddr("10.100.0.9"), netip.MustParseAddr("10.100.0.1"))
	c.SandboxRegistries, c.DNSGated, c.EgressPorts = true, true, []uint16{443}
	c.ServiceProxyHTTPS = true
	c.ServiceAddressCIDR = netip.MustParsePrefix("198.19.0.0/16")
	c.DatabaseRouteAddress = netip.MustParseAddr("198.18.0.53")
	lines := renderedLines(c)
	all := strings.Join(lines, "\n")
	pin := func(c Config) string {
		var out []string
		for _, argv := range c.GuestDNSPinCommands(func(parts ...string) []string { return parts }) {
			out = append(out, strings.Join(argv, " "))
		}
		return strings.Join(out, "\n")
	}
	if !strings.Contains(pin(c), "dnat to 10.100.0.1:"+strconv.Itoa(SandboxRegistriesDNSPort)) {
		t.Error("sandbox DNS is not pinned to the registries resolver")
	}
	bridge := "ip daddr 10.100.0.1 "
	for _, want := range []string{
		bridge + "udp dport " + strconv.Itoa(SandboxRegistriesDNSPort) + " accept",
		bridge + "tcp dport " + strconv.Itoa(SandboxRegistriesDNSPort) + " accept",
		"daddr != @" + EgressResolvedSet,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing rule %q", want)
		}
	}
	for _, port := range []int{ServiceDiscoveryDNSPort, ServiceProxyPort, LegacyServiceProxyPort, ServiceProxyHTTPSPort} {
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

	// An ordinary app keeps the tenant resolver and the service proxy.
	c.SandboxRegistries = false
	app := strings.Join(renderedLines(c), "\n")
	if !strings.Contains(pin(c), "dnat to 10.100.0.1:53") {
		t.Error("ordinary app DNS is not pinned to the tenant resolver")
	}
	for _, want := range []string{bridge + "udp dport 53 accept", bridge + "tcp dport " + strconv.Itoa(ServiceProxyPort) + " accept",
		"ip daddr 198.19.0.0/16 meta l4proto tcp accept", "ip daddr 198.18.0.53 tcp dport 5432 accept"} {
		if !strings.Contains(app, want) {
			t.Errorf("ordinary app lost %q", want)
		}
	}
	if strings.Contains(app, strconv.Itoa(SandboxRegistriesDNSPort)) {
		t.Error("ordinary app reaches the registries resolver")
	}
}
