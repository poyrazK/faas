// adr: 1349
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func TestRehearsalNetworkGuardIsIndependentOfOrdinaryPolicy(t *testing.T) {
	cfg := NewConfig("rehearsal", "test-ns", "host", "peer", netip.MustParseAddr("10.100.0.20"))
	if len(cfg.RehearsalNftCommands()) != 0 {
		t.Fatal("ordinary apps gained containment")
	}
	for _, argv := range cfg.NftCommands() {
		if strings.Contains(strings.Join(argv, " "), "tcp dport 10083 accept") || strings.Contains(strings.Join(argv, " "), "tcp dport 10084 accept") {
			t.Fatal("ordinary apps gained the rehearsal PostgreSQL port")
		}
	}
	cfg.RehearsalRunID = strings.Repeat("a", 32)
	cfg.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	cfg.OperatorExceptions = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cfg.EgressPorts = []uint16{80, 443, 5432}
	cfg.ServiceProxyHTTPS = true
	var guard strings.Builder
	for _, argv := range cfg.RehearsalNftCommands() {
		guard.WriteString(strings.Join(argv, " "))
		guard.WriteByte('\n')
	}
	for _, want := range []string{"hook forward priority -10 ; policy drop", "ct direction reply ct state established,related accept", "tcp dport 10081 accept", "tcp dport 10083 accept", "tcp dport 10084 accept", "udp dport 5354 accept", "dnat ip to 10.100.0.1:5354", "ip saddr 10.0.0.2", "ct status dnat accept"} {
		if !strings.Contains(guard.String(), want) {
			t.Errorf("missing containment rule: %s", want)
		}
	}
	for _, forbidden := range []string{"tcp dport 443", "tcp dport 5432", "10.0.0.0/8", "198.51.100.0/24", "tcp dport 10080", "udp dport 53 accept", "tcp dport 53 accept"} {
		if strings.Contains(guard.String(), forbidden) {
			t.Errorf("ordinary exception reached guard: %s", forbidden)
		}
	}
	for _, argv := range cfg.NftResetCommands() {
		if strings.Contains(strings.Join(argv, " "), "gregale_rehearsal") {
			t.Fatal("ordinary reset deletes immutable guard")
		}
	}
	full := cfg.NftCommands()
	postgresBaseRule := false
	objectBaseRule := false
	for _, argv := range full {
		if strings.Contains(strings.Join(argv, " "), "add rule ip faas forward iifname tap0 ip daddr 10.100.0.1 tcp dport 10084 accept") {
			objectBaseRule = true
		}
		if strings.Contains(strings.Join(argv, " "), "add rule ip faas forward iifname tap0 ip daddr 10.100.0.1 tcp dport 10083 accept") {
			postgresBaseRule = true
		}
	}
	if !objectBaseRule {
		t.Fatal("ordinary firewall chain still blocks contained object storage")
	}
	if !postgresBaseRule {
		t.Fatal("ordinary firewall chain still blocks contained PostgreSQL")
	}
	if len(full) == 0 || !strings.Contains(strings.Join(full[len(full)-1], " "), "gregale_rehearsal") {
		t.Fatal("full render omits independent guard")
	}
}
