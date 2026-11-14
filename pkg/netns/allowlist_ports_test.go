package netns

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// An allowlist narrows egress; it must not reopen the ADR-361 abuse ports. An
// app that allowlists 1.0.0.0/8 still cannot reach port 22 or 3333 there,
// while SMTP submission (465/587) stays open to listed destinations and port
// 25 stays closed (bug hunt H69).
func TestAllowlistKeepsForbiddenPortsClosed(t *testing.T) {
	c := Config{Tap: "tap0", EgressAllowlist: []netip.Prefix{netip.MustParsePrefix("1.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}}
	nft := func(args ...string) []string { return args }
	set := AllowlistBlockedPortsSet()
	ports := strings.Split(set, ",")
	for _, closed := range []int{22, 25, 3333, 445, 6667, 53} {
		if !slices.Contains(ports, strconv.Itoa(closed)) {
			t.Errorf("port %d is open to allowlisted destinations", closed)
		}
	}
	for _, open := range []int{465, 587, 443} {
		if slices.Contains(ports, strconv.Itoa(open)) {
			t.Errorf("port %d is closed to allowlisted destinations", open)
		}
	}
	for name, rule := range map[string][]string{"v4": c.ForwardAllowlistRule(nft), "v6": c.ForwardAllowlistRule6(nft)} {
		line := strings.Join(rule, " ")
		if !strings.Contains(line, "tcp dport != { "+set+" } accept") {
			t.Errorf("%s allowlist rule does not exclude the forbidden ports: %s", name, line)
		}
	}
}
