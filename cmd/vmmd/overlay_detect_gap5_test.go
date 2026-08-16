// Gap #5 overlay-interface pin tests. PR scale-out tier-1
// residual: when PinnedInterface is set, the detector pins to
// that NIC's IPv4 address and falls back to PreferCIDR scoring
// when the pinned NIC is missing or has no IPv4 address.
//
// The test seam (pinnedInterfaceIPFunc) is a package-private
// var that production reads from cmd/vmmd/overlay_detect.go's
// detectOverlayIP path; tests swap it for a stub that returns
// canned values so the detector can be exercised without
// shelling out to `ip -4 -o addr show dev <iface>`. The
// per-line parser is exposed via parseIPAddrShowLine so it can
// be tested directly without the scanner / exec.

package main

import (
	"context"
	"net/netip"
	"testing"
)

// TestDetectOverlayIP_PinnedInterfaceWinsOverCIDRScoring
// exercises the load-bearing Gap #5 branch: when both
// PinnedInterface AND PreferCIDR are set, the pinned NIC's
// IPv4 wins — operator intent overrides CIDR scoring. The
// tailscale Run field is NOT invoked because the pin path
// short-circuits before it.
func TestDetectOverlayIP_PinnedInterfaceWinsOverCIDRScoring(t *testing.T) {
	saved := pinnedInterfaceIPFunc
	defer func() { pinnedInterfaceIPFunc = saved }()

	pinnedInterfaceIPFunc = func(_ context.Context, iface string) (netip.Addr, bool, error) {
		if iface != "tailscale0" {
			t.Fatalf("pinned run got iface %q, want tailscale0", iface)
		}
		return netip.MustParseAddr("100.100.100.7"), true, nil
	}

	det := OverlayDetector{
		TailscaleBinaryPath: "/nonexistent",
		PreferCIDR:          netip.MustParsePrefix("100.64.0.0/10"),
		PinnedInterface:     "tailscale0",
		Run: func(_ context.Context) ([]byte, error) {
			t.Fatalf("Run should not be called when the pin path succeeds")
			return nil, nil
		},
	}
	got, err := detectOverlayIP(context.Background(), det)
	if err != nil {
		t.Fatalf("detectOverlayIP: %v", err)
	}
	if got != "100.100.100.7" {
		t.Errorf("overlay IP = %q, want 100.100.100.7 (the pinned NIC's IPv4)", got)
	}
}

// TestDetectOverlayIP_PinnedInterfaceMissingNICFallsBackToScoring
// asserts the v1 contract: when the pinned NIC has no IPv4
// address (the stub returns ok=false), the detector falls
// through to the PreferCIDR scoring path. The Tailscale-stubbed
// output MUST be present in this branch — proves the
// fall-through wiring is correct.
func TestDetectOverlayIP_PinnedInterfaceMissingNICFallsBackToScoring(t *testing.T) {
	saved := pinnedInterfaceIPFunc
	defer func() { pinnedInterfaceIPFunc = saved }()

	stub := []byte("100.64.0.5\n192.168.1.10\n")
	runCalled := false
	pinnedInterfaceIPFunc = func(_ context.Context, _ string) (netip.Addr, bool, error) {
		return netip.Addr{}, false, nil // pinned NIC has no IPv4
	}

	det := OverlayDetector{
		TailscaleBinaryPath: "/not-used",
		PreferCIDR:          netip.MustParsePrefix("100.64.0.0/10"),
		PinnedInterface:     "wg-not-there",
		Run: func(_ context.Context) ([]byte, error) {
			runCalled = true
			return stub, nil
		},
	}
	got, err := detectOverlayIP(context.Background(), det)
	if err != nil {
		t.Fatalf("detectOverlayIP: %v", err)
	}
	if !runCalled {
		t.Fatalf("Run was not called; the detector should have fallen through to PreferCIDR scoring")
	}
	if got != "100.64.0.5" {
		t.Errorf("overlay IP = %q, want 100.64.0.5 (the PreferCIDR match after fall-through)", got)
	}
}

// TestDefaultDetectOverlayIP_HonorsOverlayInterfaceFromConfig
// pins the wiring: cfg.OverlayInterface flows into the
// detector's PinnedInterface AND the detector pins to that
// NIC's IPv4. The test invokes defaultDetectOverlayIP after
// stubbing pinnedInterfaceIPFunc, so the cfg→detector wiring
// is exercised end-to-end (not just a struct copy).
func TestDefaultDetectOverlayIP_HonorsOverlayInterfaceFromConfig(t *testing.T) {
	saved := pinnedInterfaceIPFunc
	defer func() { pinnedInterfaceIPFunc = saved }()

	pinnedInterfaceIPFunc = func(_ context.Context, iface string) (netip.Addr, bool, error) {
		if iface != "tailscale0" {
			t.Fatalf("pinned run got iface %q, want tailscale0 (cfg.OverlayInterface must flow through)", iface)
		}
		return netip.MustParseAddr("100.100.100.42"), true, nil
	}

	cfg := ComputeNodeConfig{
		OverlayInterface: "tailscale0",
		// TailscaleBinaryPath is intentionally left empty so the
		// production exec.LookPath("tailscale") runs and returns
		// ErrNotFound — confirms the pinned path wins regardless
		// of whether tailscale is installed.
	}
	got, err := defaultDetectOverlayIP(context.Background(), cfg)
	if err != nil {
		t.Fatalf("defaultDetectOverlayIP: %v", err)
	}
	if got != "100.100.100.42" {
		t.Errorf("overlay IP = %q, want 100.100.100.42 (the pinned NIC's IPv4 from cfg)", got)
	}
}

// TestParseIPAddrShowLine_Valid is the parser test for the
// `ip -4 -o addr show dev <iface>` output. Each line of that
// output looks like:
//
//	<idx>: <iface> inet <addr>/<mask> brd ...
//
// The parser MUST pull the IPv4 address out of the field
// after `inet`. This is the load-bearing code path that
// detector pins depend on — a silently-wrong parser would
// make the operator-pinned NIC return the wrong IP across
// the entire overlay chain.
func TestParseIPAddrShowLine_Valid(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
		ok   bool
	}{
		{
			name: "single line with /24 mask",
			line: "3: tailscale0    inet 100.100.100.7/24 brd 100.100.100.255 scope global tailscale0",
			want: "100.100.100.7",
			ok:   true,
		},
		{
			name: "single line with /32 mask",
			line: "2: eth0    inet 10.42.0.5/32 brd 10.42.0.5 scope global eth0",
			want: "10.42.0.5",
			ok:   true,
		},
		{
			name: "single line with /16 mask",
			line: "4: eth1    inet 192.168.1.10/16 brd 192.168.255.255 scope global eth1",
			want: "192.168.1.10",
			ok:   true,
		},
		{
			name: "v6-only line is skipped, returns false",
			line: "5: tailscale0    inet6 fd7a:115c:a1e0::1/64 scope global",
			want: "",
			ok:   false,
		},
		{
			name: "no inet field at all",
			line: "1: lo    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00",
			want: "",
			ok:   false,
		},
		{
			name: "malformed inet value",
			line: "2: eth0    inet not-an-ip/24 brd 0.0.0.0 scope global eth0",
			want: "",
			ok:   false,
		},
		{
			name: "empty line",
			line: "",
			want: "",
			ok:   false,
		},
		{
			name: "inet6 + inet on separate fields — first hit wins",
			line: "2: eth0    inet 10.0.0.5/24 inet6 fe80::1/64",
			want: "10.0.0.5",
			ok:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseIPAddrShowLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.ok, got)
			}
			if !ok {
				return
			}
			if got.String() != tc.want {
				t.Errorf("addr = %q, want %q", got, tc.want)
			}
		})
	}
}
