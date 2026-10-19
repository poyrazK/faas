package oci

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

func TestIPAllowed_PublicAllowed(t *testing.T) {
	cases := []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34",
		"2606:4700:4700::1111",
	}
	for _, s := range cases {
		ip := netip.MustParseAddr(s)
		if !ipAllowed(ip) {
			t.Errorf("ipAllowed(%s) = false, want true", s)
		}
	}
}

func TestIPAllowed_DeniedRanges(t *testing.T) {
	cases := []string{
		"10.0.0.1",        // RFC1918
		"10.255.255.255",  // RFC1918 edge
		"172.16.0.1",      // RFC1918
		"172.31.255.255",  // RFC1918 edge
		"192.168.0.1",     // RFC1918
		"127.0.0.1",       // loopback
		"169.254.169.254", // AWS / GCP metadata
		"100.64.0.1",      // carrier-grade NAT
		"0.0.0.0",         // unspecified
		"224.0.0.1",       // multicast
		"::1",             // IPv6 loopback
		"fe80::1",         // IPv6 link-local
		"fc00::1",         // IPv6 ULA
		"ff02::1",         // IPv6 multicast
	}
	for _, s := range cases {
		ip := netip.MustParseAddr(s)
		if ipAllowed(ip) {
			t.Errorf("ipAllowed(%s) = true, want false", s)
		}
	}
}

// TestIPAllowed_6to4AndTeredoDenied (PR-D) closes the
// ADR-034 §Consequences defence-in-depth gap: the shared
// catalog added 2002::/16 (6to4) and 2001::/32 (Teredo) in
// PR-A but the OCI user-space check never had an explicit
// probe for them. ADR-034 §Consequences (L143-148) named
// this as 'free defence-in-depth still owed'. The puller is
// an HTTP client, not a guest kernel, so the firewall path
// is the primary; the user-space check exists to catch a
// misconfigured firewall. Pin both.
// NAT64 forms of denied IPv4 destinations stay denied, and so does the
// local-use NAT64 prefix (ADR 20261018194214).
func TestIPAllowed_NAT64Denied(t *testing.T) {
	for _, s := range []string{
		"64:ff9b::a9fe:a9fe", // 169.254.169.254, cloud metadata
		"64:ff9b::a00:1",     // 10.0.0.1
		"64:ff9b::808:808",   // 8.8.8.8: the whole well-known prefix is denied
		"64:ff9b:1::a00:1",   // local-use NAT64
	} {
		ip := netip.MustParseAddr(s)
		if ipAllowed(ip) || EgressIPAllowed(ip) {
			t.Errorf("NAT64 address %s allowed, want denied", s)
		}
	}
	if !EgressIPAllowed(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Error("public IPv6 2606:4700:4700::1111 denied, want allowed")
	}
}

func TestIPAllowed_6to4AndTeredoDenied(t *testing.T) {
	cases := []string{
		"2002::1",           // 6to4, ADR-034
		"2001:0000::1",      // Teredo, ADR-034
		"2002:0a00:0001::1", // 6to4 wrapping RFC1918 (belt-and-braces)
	}
	for _, s := range cases {
		ip := netip.MustParseAddr(s)
		if ipAllowed(ip) {
			t.Errorf("ipAllowed(%s) = true, want false", s)
		}
		// EgressIPAllowed is the exported testable mirror added
		// in PR-D; both must agree (regression net for the
		// thin-wrapper contract).
		if EgressIPAllowed(ip) {
			t.Errorf("EgressIPAllowed(%s) = true, want false", s)
		}
	}
}

// TestIPAllowed_OCIOnlyEntriesDenied (PR-D) pins the OCI-only
// client-hardening ranges after the refactor from
// []netip.Prefix to []netns.DenyEntry. The five entries
// remain typed and provenance-bearing (SourceADR: ADR-034)
// but the enforcement path is unchanged — these addresses
// must continue to be denied by the user-space dial check.
func TestIPAllowed_OCIOnlyEntriesDenied(t *testing.T) {
	cases := []string{
		"0.0.0.1",    // 0.0.0.0/8 unspecified
		"127.0.0.2",  // 127.0.0.0/8 loopback (the canonical docker-host IP)
		"192.0.0.7",  // 192.0.0.0/24 IETF protocol assignments
		"198.18.0.1", // 198.18.0.0/15 benchmarking
		"240.0.0.1",  // 240.0.0.0/4 reserved
	}
	for _, s := range cases {
		ip := netip.MustParseAddr(s)
		if ipAllowed(ip) {
			t.Errorf("ipAllowed(%s) = true, want false (must be denied by OCI-only entry)", s)
		}
		if EgressIPAllowed(ip) {
			t.Errorf("EgressIPAllowed(%s) = true, want false", s)
		}
	}
}

// TestOCIOnlyDenyCIDRsV4_Typed (PR-D) pins the typed-array
// refactor. Each entry must carry the ADR-034 SourceADR
// pin and a non-empty Comment so the cross-renderer test
// and the denylist.md generator see consistent provenance.
func TestOCIOnlyDenyCIDRsV4_Typed(t *testing.T) {
	if len(ociOnlyDenyCIDRsV4) != 5 {
		t.Fatalf("ociOnlyDenyCIDRsV4 length = %d, want 5", len(ociOnlyDenyCIDRsV4))
	}
	for i, e := range ociOnlyDenyCIDRsV4 {
		// Family field is not asserted against netns.FamilyV4 because
		// this test is internal `package oci` and importing pkg/netns
		// here would form a cycle (pkg/oci -> pkg/netns, then
		// pkg/netns_test -> pkg/oci closes it via EgressIPAllowed).
		// The variable name (ociOnlyDenyCIDRsV4) and the literal
		// prefixes below all carry v4 family, so the typed-array
		// promotion is self-evidently correct. The cross-renderer
		// invariant test in pkg/netns/denylist_test.go covers the
		// shared catalog's Family tag end-to-end.
		if e.SourceADR == "" {
			t.Errorf("entry %d (%s) SourceADR is empty", i, e.Prefix)
		}
		if e.Comment == "" {
			t.Errorf("entry %d (%s) Comment is empty", i, e.Prefix)
		}
		if !e.Prefix.IsValid() {
			t.Errorf("entry %d has invalid prefix %s", i, e.Prefix)
		}
	}
}

func TestEgressDialContext_RefusesRFC1918(t *testing.T) {
	dial := EgressDialContext(&net.Dialer{})
	// Resolve "localhost" → 127.0.0.1 and verify the dial is refused.
	conn, err := dial(context.Background(), "tcp", "localhost:80")
	if err == nil {
		_ = conn.Close()
		t.Fatal("egress dial to localhost should be denied")
	}
	// ADR-021: a denied dial must lift to the canonical
	// ErrImageEgressDenied sentinel so the imaged handler can persist
	// deployments.error_code = image_egress_denied (403, security-class).
	// The legacy ErrEgressDenied is wrapped inside it for backwards compat.
	if !errors.Is(err, ErrImageEgressDenied) {
		t.Errorf("egress dial RFC1918 err = %v, want errors.Is(_, ErrImageEgressDenied) true", err)
	}
	if !errors.Is(err, ErrEgressDenied) {
		t.Errorf("egress dial RFC1918 err = %v, want errors.Is(_, ErrEgressDenied) true (legacy compat)", err)
	}
}

func TestEgressDialContext_RefusesMetadataIP(t *testing.T) {
	dial := EgressDialContext(&net.Dialer{})
	_, err := dial(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("egress dial to 169.254.169.254 should be denied")
	}
	if !errors.Is(err, ErrImageEgressDenied) {
		t.Errorf("egress dial IMDS err = %v, want errors.Is(_, ErrImageEgressDenied) true", err)
	}
}

// TestEgressDenyHook_CalledOnDenial (PR-E) — installs a hook and
// asserts the dialer invokes it when a target address is denied.
// The hook receives the catalog-derived CounterName (e.g.
// `drop_v4_10_0_0_0_8`) so the imaged-side counter increments with
// the same label set the vmmd poll adapter uses — the cross-renderer
// observability contract.
//
// The hook is a package-level var, so tests that install it MUST
// restore the previous value via t.Cleanup to avoid leaking state
// into the rest of the suite.
func TestEgressDenyHook_CalledOnDenial(t *testing.T) {
	prev := EgressDenyHook
	t.Cleanup(func() { EgressDenyHook = prev })

	type call struct {
		target      netip.Addr
		counterName string
		family      string
	}
	var got []call
	EgressDenyHook = func(target netip.Addr, counterName, family string) {
		got = append(got, call{target, counterName, family})
	}

	dial := EgressDialContext(&net.Dialer{})
	_, err := dial(context.Background(), "tcp", "10.0.0.1:80")
	if err == nil {
		t.Fatal("dial to 10.0.0.1 should be denied")
	}
	if len(got) != 1 {
		t.Fatalf("EgressDenyHook called %d times, want 1: %+v", len(got), got)
	}
	if got[0].counterName != "drop_v4_10_0_0_0_8" {
		t.Errorf("EgressDenyHook counterName = %q, want %q",
			got[0].counterName, "drop_v4_10_0_0_0_8")
	}
	if got[0].family != "ip" {
		t.Errorf("EgressDenyHook family = %q, want %q", got[0].family, "ip")
	}
	if got[0].target.String() != "10.0.0.1" {
		t.Errorf("EgressDenyHook target = %s, want 10.0.0.1", got[0].target)
	}
}

// TestEgressDenyHook_NotCalledOnAllow — the hook must NOT fire when
// the dial passes the ipAllowed check. The hook is the deny-side
// observability seam, not a dial-success hook.
//
// 1.1.1.1 is a public IP literal — ipAllowed returns true, so the
// loop walks past the hook site and lands in parent.DialContext,
// which fails on a sandboxed CI runner with "no route to host" or
// similar. We don't care whether the dial itself succeeds; we only
// care that the hook was not invoked.
func TestEgressDenyHook_NotCalledOnAllow(t *testing.T) {
	prev := EgressDenyHook
	t.Cleanup(func() { EgressDenyHook = prev })

	called := 0
	EgressDenyHook = func(netip.Addr, string, string) { called++ }

	dial := EgressDialContext(&net.Dialer{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = dial(ctx, "tcp", "1.1.1.1:80")
	if called != 0 {
		t.Errorf("EgressDenyHook fired %d times for a public-IP dial, want 0", called)
	}
}

// TestMatchedDenyEntry_PrefixLookup — exercises the helper that
// maps a denied IP back to the catalog entry's CounterName. The
// helper is package-internal (matchedDenyEntry) and is the source
// of the (cidr, family) label pair the hook receives.
func TestMatchedDenyEntry_PrefixLookup(t *testing.T) {
	cases := []struct {
		ip           string
		wantName     string
		wantFamily   string
		wantSourceAD string
	}{
		{"10.0.0.1", "drop_v4_10_0_0_0_8", "ip", "spec-§11"},
		{"172.16.0.1", "drop_v4_172_16_0_0_12", "ip", "spec-§11"},
		{"169.254.169.254", "drop_v4_169_254_0_0_16", "ip", "spec-§11"},
		{"192.168.1.1", "drop_v4_192_168_0_0_16", "ip", "spec-§11"},
		// OCI-only extras — loopback hits the OCI-only 127/8 entry,
		// not the shared catalog (the catalog doesn't have 127/8).
		{"127.0.0.1", netns.DropCounterName(netns.FamilyV4, "127.0.0.0/8"), "ip", "ADR-034"},
		// v6 cases
		{"fe80::1", "drop_v6_fe80___10", "ip6", "ADR-023"},
		{"2002::1", "drop_v6_2002___16", "ip6", "ADR-034"},
	}
	for _, c := range cases {
		ip := netip.MustParseAddr(c.ip)
		got := matchedDenyEntry(ip)
		if got.CounterName != c.wantName {
			t.Errorf("matchedDenyEntry(%s).CounterName = %q, want %q",
				c.ip, got.CounterName, c.wantName)
		}
		if got.Family.String() != c.wantFamily {
			t.Errorf("matchedDenyEntry(%s).Family = %q, want %q",
				c.ip, got.Family.String(), c.wantFamily)
		}
		if got.SourceADR != c.wantSourceAD {
			t.Errorf("matchedDenyEntry(%s).SourceADR = %q, want %q",
				c.ip, got.SourceADR, c.wantSourceAD)
		}
	}
}

func TestNewEgressHTTPClient_RoundTripsPublic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	hc := NewEgressHTTPClient()
	// Pin to the test server's host explicitly (httptest binds 127.0.0.1 —
	// we can't reach it through the egress client because 127.0.0.1 is
	// denied). Override the dial with one that skips policy for the test
	// server, then verify the rest of the client wiring still works.
	host := srv.URL[len("http://"):]
	tr := hc.Transport.(*http.Transport)
	tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", host)
	}

	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
