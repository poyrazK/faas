// adr: 20261031193635
package netns

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func joinCommands(cmds [][]string) string {
	var b strings.Builder
	for _, argv := range cmds {
		b.WriteString(strings.Join(argv, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestEnvironmentBranchContainedProfile(t *testing.T) {
	cfg := NewConfig("branch", "test-ns", "host", "peer", netip.MustParseAddr("10.100.0.20"))
	if len(cfg.EnvironmentBranchNftCommands()) != 0 || strings.Contains(joinCommands(cfg.NftCommands()), EnvironmentBranchTable) {
		t.Fatal("an ordinary instance gained the contained branch profile")
	}
	if strings.Contains(joinCommands(cfg.NftCommands()), "dport 10085") {
		t.Fatal("an ordinary instance reaches the bridge object relay")
	}
	cfg.EnvironmentBranchContained = true
	cfg.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	cfg.OperatorExceptions = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cfg.EgressPorts = []uint16{80, 443, 5432}
	cfg.ServiceProxyHTTPS = true
	guard := joinCommands(cfg.EnvironmentBranchNftCommands())
	for _, want := range []string{
		"add table inet gregale_branch",
		"add set inet gregale_branch branch_db { type ipv4_addr . inet_service ; flags timeout ; }",
		"hook forward priority -10 ; policy drop",
		"flush chain inet gregale_branch forward",
		"ct direction reply ct state established,related accept",
		"ct status dnat accept",
		"ip daddr 10.100.0.1 tcp dport 10081 accept",
		"ip daddr 10.100.0.1 tcp dport 10085 accept",
		"ip daddr 10.100.0.1 tcp dport 53 accept",
		"ip daddr 10.100.0.1 udp dport 53 accept",
		"ip saddr 10.0.0.2 ip daddr . tcp dport @branch_db accept",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("missing contained rule: %s", want)
		}
	}
	// The table is never flushed whole: that would empty branch_db.
	if strings.Contains(guard, "flush table") || strings.Contains(guard, "delete table") {
		t.Error("a re-render empties the branch database set")
	}
	for _, forbidden := range []string{"tcp dport 443", "tcp dport 5432", "10.0.0.0/8", "198.51.100.0/24", "tcp dport 10080", "10082", "10083", "10084", "ip6"} {
		if strings.Contains(guard, forbidden) {
			t.Errorf("an ordinary exception reached the contained profile: %s", forbidden)
		}
	}
	if strings.Contains(joinCommands(cfg.NftResetCommands()), EnvironmentBranchTable) {
		t.Fatal("an ordinary reset deletes the contained profile")
	}
	full := cfg.NftCommands()
	if !strings.Contains(joinCommands(full[len(full)-len(cfg.EnvironmentBranchNftCommands()):]), "policy drop") {
		t.Fatal("the full render omits the contained profile")
	}
	// The ordinary forward chain admits the relay port too, or the contained
	// accept would be dropped there.
	if !strings.Contains(joinCommands(full), "ip faas forward iifname tap0 ip daddr 10.100.0.1 tcp dport 10085 accept") {
		t.Fatal("the ordinary chain drops the bridge object relay")
	}
}

func TestEnvironmentBranchDatabaseAddCommands(t *testing.T) {
	cfg := NewConfig("branch", "test-ns", "host", "peer", netip.MustParseAddr("10.100.0.20"))
	addrs := []netip.Addr{netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::7"), netip.MustParseAddr("::ffff:203.0.113.8")}
	if cmds := cfg.EnvironmentBranchDatabaseAddCommands(addrs, 5432, time.Minute); len(cmds) != 0 {
		t.Fatalf("uncontained instance got branch_db elements: %v", cmds)
	}
	cfg.EnvironmentBranchContained = true
	if cmds := cfg.EnvironmentBranchDatabaseAddCommands(addrs, 0, time.Minute); len(cmds) != 0 {
		t.Fatalf("port 0 added elements: %v", cmds)
	}
	got := joinCommands(cfg.EnvironmentBranchDatabaseAddCommands(addrs, 5432, 90*time.Second))
	want := "ip netns exec test-ns nft add element inet gregale_branch branch_db { 203.0.113.7 . 5432 timeout 90s, 203.0.113.8 . 5432 timeout 90s }\n"
	if got != want {
		t.Fatalf("add commands = %q, want %q", got, want)
	}
	if cmds := cfg.EnvironmentBranchDatabaseAddCommands(addrs[1:2], 5432, time.Minute); len(cmds) != 0 {
		t.Fatalf("an IPv6-only answer added elements: %v", cmds)
	}
}
