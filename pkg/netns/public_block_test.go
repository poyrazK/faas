package netns

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestParsePublicBlockList(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	data := []byte(`# flood from one range, 2026-11-04
192.0.2.0/24 2026-11-04T13:00:00Z syn flood
198.51.100.7/32 2026-11-04T12:00:30.5Z
2001:db8::/48 2027-11-04T12:00:00Z a year, capped

203.0.113.0/24 2026-11-04T11:00:00Z lapsed
203.0.113.9/24 2026-11-04T13:00:00Z not canonical
203.0.113.0/24
not-a-prefix 2026-11-04T13:00:00Z
203.0.113.0/24 tomorrow
`)
	entries, errs := ParsePublicBlockList(data, now)
	want := []PublicBlock{
		{netip.MustParsePrefix("192.0.2.0/24"), time.Hour},
		{netip.MustParsePrefix("198.51.100.7/32"), 30*time.Second + 500*time.Millisecond},
		{netip.MustParsePrefix("2001:db8::/48"), publicBlockTimeoutMax},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v, want %v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d = %v, want %v", i, entries[i], want[i])
		}
	}
	if len(errs) != 4 {
		t.Fatalf("errs = %v, want 4 (non-canonical, missing expiry, bad prefix, bad expiry)", errs)
	}
	for i, line := range []string{"line 7", "line 8", "line 9", "line 10"} {
		if !strings.Contains(errs[i].Error(), line) {
			t.Errorf("err %d = %v, want it to name %s", i, errs[i], line)
		}
	}
}

// Elements go only to the sets the policy declares, with timeouts rounded up
// to whole seconds.
func TestPublicBlockScript(t *testing.T) {
	entries := []PublicBlock{
		{netip.MustParsePrefix("192.0.2.0/24"), time.Hour},
		{netip.MustParsePrefix("198.51.100.7/32"), 30*time.Second + 500*time.Millisecond},
		{netip.MustParsePrefix("2001:db8::/48"), time.Minute},
	}
	v4 := []netip.Addr{netip.MustParseAddr("203.0.113.10")}
	got := PublicBlockScript(entries, v4)
	want := "add element inet faas public_block_v4 { 192.0.2.0/24 timeout 3600s }\n" +
		"add element inet faas public_block_v4 { 198.51.100.7/32 timeout 31s }\n"
	if got != want {
		t.Fatalf("script =\n%s\nwant\n%s", got, want)
	}
	both := append(v4, netip.MustParseAddr("2001:db8::10"))
	if got := PublicBlockScript(entries, both); !strings.Contains(got, "add element inet faas public_block_v6 { 2001:db8::/48 timeout 60s }\n") {
		t.Fatalf("dual-stack script lacks the v6 element:\n%s", got)
	}
	if got := PublicBlockScript(entries, nil); got != "" {
		t.Fatalf("a node with no entry address got a script:\n%s", got)
	}
}

// The rendered policy declares the block sets and drops their sources on the
// entry addresses ahead of the established accept, so a blocked source's open
// connections stop too.
func TestHostPolicyPublicEntryBlockListDropsFirst(t *testing.T) {
	p := DefaultHostPolicy
	p.PublicEntryAddrs = []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}
	out := p.Render()
	for _, set := range []string{
		"  set public_block_v4 {\n    type ipv4_addr\n    flags interval,timeout\n  }\n",
		"  set public_block_v6 {\n    type ipv6_addr\n    flags interval,timeout\n  }\n",
	} {
		if !strings.Contains(out, set) {
			t.Fatalf("policy lacks %q:\n%s", set, out)
		}
	}
	block := strings.Index(out, `ip daddr 203.0.113.10 ip saddr @public_block_v4 counter drop comment "public entry block list"`)
	block6 := strings.Index(out, `ip6 daddr 2001:db8::10 ip6 saddr @public_block_v6 counter drop comment "public entry block list"`)
	established := strings.Index(out, "ct state established,related accept")
	if block < 0 || block6 < 0 || established < 0 || block > established || block6 > established {
		t.Fatalf("block-list drops missing or after the established accept:\n%s", out)
	}
	if out := DefaultHostPolicy.Render(); strings.Contains(out, "public_block_") {
		t.Fatalf("a policy with no entry address declares block sets:\n%s", out)
	}
}
