// adr: 176
// Tests for the sidecar portnorm routing-key split
// (issue #463 / ADR-069 / ADR-071 / PR-C §5). The public
// listener's hostname carries both the app id AND the
// (optional) sidecar name; the `--` separator splits them.
// The handler then resolves (appHost, sidecarName) to
// (App.ID, sidecarPort) so the forwarder can route to the
// right port on the same instance.

package gateway

import "testing"

// TestSplitHostSelector_NoSidecar pins the legacy
// single-app routing: a hostname without `--` resolves to
// (host, "") — the main workload's port (Target.Port, 0 =
// netns.AppPort = 8080).
func TestSplitHostSelector_NoSidecar(t *testing.T) {
	cases := []struct {
		in, wantHost, wantSidecar string
	}{
		{"acme.on-faas.com", "acme.on-faas.com", ""},
		{"acme-staging.on-faas.com", "acme-staging.on-faas.com", ""},
		{"localhost", "localhost", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			gotHost, gotSidecar := SplitHostSelector(c.in)
			if gotHost != c.wantHost {
				t.Errorf("host = %q, want %q", gotHost, c.wantHost)
			}
			if gotSidecar != c.wantSidecar {
				t.Errorf("sidecar = %q, want %q", gotSidecar, c.wantSidecar)
			}
		})
	}
}

// TestSplitHostSelectorWithSidecar_PublicListener pins the
// production hostname → (appHost, sidecar) split with the
// suffix configured. The split runs at the FIRST `--`
// after the suffix is stripped; the app host is
// re-attached with the suffix so the routing-cache key
// matches the apps row stored at provision time.
func TestSplitHostSelectorWithSidecar_PublicListener(t *testing.T) {
	const suffix = ".on-faas.com"
	cases := []struct {
		in, wantHost, wantSidecar string
	}{
		// Legacy single-app hostname — no `--`, no sidecar.
		{"acme" + suffix, "acme" + suffix, ""},
		// Sidecar selector on a real suffix.
		{"acme--metrics" + suffix, "acme" + suffix, "metrics"},
		// Sidecar selector on a deployment that has the
		// host registered with a different segment.
		{"acme-staging--audit" + suffix, "acme-staging" + suffix, "audit"},
		// No suffix on input — listener suffix-gate 404s
		// upstream but the split doesn't enforce it.
		{"acme--audit", "acme", "audit"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			gotHost, gotSidecar := SplitHostSelectorWithSuffix(c.in, suffix)
			if gotHost != c.wantHost {
				t.Errorf("host = %q, want %q", gotHost, c.wantHost)
			}
			if gotSidecar != c.wantSidecar {
				t.Errorf("sidecar = %q, want %q", gotSidecar, c.wantSidecar)
			}
		})
	}
}

// TestSidecarSelectorForApp_Main pins the empty-
// sidecarName branch: the port is 0 (Target.Port wins —
// resolved by the forwarder to netns.AppPort 8080 for
// legacy targets).
func TestSidecarSelectorForApp_Main(t *testing.T) {
	app := App{
		ID: "app-1",
		Sidecars: []AppSidecar{
			{Name: "metrics", Port: 9100},
		},
	}
	port, ok := SidecarSelectorForApp(app, "")
	if !ok {
		t.Fatal("SidecarSelectorForApp returned !ok for empty sidecarName")
	}
	if port != 0 {
		t.Errorf("port = %d; want 0 (main workload)", port)
	}
}

// TestSidecarSelectorForApp_KnownSidecar pins the
// success path: a sidecarName matching a roster row
// resolves to that sidecar's port.
func TestSidecarSelectorForApp_KnownSidecar(t *testing.T) {
	app := App{
		ID: "app-1",
		Sidecars: []AppSidecar{
			{Name: "metrics", Port: 9100},
			{Name: "audit", Port: 9091},
		},
	}
	cases := []struct {
		name     string
		wantPort int
	}{
		{"metrics", 9100},
		{"audit", 9091},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			port, ok := SidecarSelectorForApp(app, c.name)
			if !ok {
				t.Fatalf("SidecarSelectorForApp returned !ok for %q", c.name)
			}
			if port != c.wantPort {
				t.Errorf("port = %d; want %d", port, c.wantPort)
			}
		})
	}
}

// TestSidecarSelectorForApp_UnknownSidecar pins the
// 404-bound path: a sidecarName that doesn't match a
// roster row returns (0, false) so the handler can surface
// the canonical "No such sidecar" problem.
func TestSidecarSelectorForApp_UnknownSidecar(t *testing.T) {
	app := App{
		ID: "app-1",
		Sidecars: []AppSidecar{
			{Name: "metrics", Port: 9100},
		},
	}
	_, ok := SidecarSelectorForApp(app, "logger")
	if ok {
		t.Errorf("SidecarSelectorForApp returned ok=true for unknown sidecarName; want ok=false")
	}
}

// TestSidecarSelectorForApp_NoSidecarsRoster pins the
// zero-sidecars deployment: a sidecarName request to a
// no-sidecars app resolves to (0, false). Pre-PR-B
// installs hit this path; the handler 404s the request
// with `No such sidecar` so customers see the right
// diagnostic, not a confusing `no app is routed`.
func TestSidecarSelectorForApp_NoSidecarsRoster(t *testing.T) {
	app := App{ID: "app-legacy"}
	_, ok := SidecarSelectorForApp(app, "metrics")
	if ok {
		t.Errorf("SidecarSelectorForApp returned ok=true for no-sidecars app; want ok=false")
	}
}

// TestSidecarHostSeparatorConstant pins the separator
// literal so a future rename is caught at compile + test.
// The dashboard, the provisioner, and the test suite all
// reach into the same constant; drift here is silent
// (every selector would 404 the customer but the box is
// still healthy).
func TestSidecarHostSeparatorConstant(t *testing.T) {
	if SidecarHostSeparator != "--" {
		t.Errorf("SidecarHostSeparator = %q; want \"--\"", SidecarHostSeparator)
	}
}

func TestPublicPortSelectorForApp(t *testing.T) {
	app := App{Ports: []AppPort{
		{Name: "metrics", Port: 9100, Protocol: "tcp"},
		{Name: "dns", Port: 53, Protocol: "udp"},
		{Port: 9000, Protocol: "tcp"},
	}}
	tests := []struct {
		selector string
		wantPort int
		wantOK   bool
	}{
		{selector: "port-metrics", wantPort: 9100, wantOK: true},
		{selector: "port-tcp-9000", wantPort: 9000, wantOK: true},
		{selector: "port-dns", wantOK: false},
		{selector: "port-unknown", wantOK: false},
		{selector: "metrics", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.selector, func(t *testing.T) {
			gotPort, gotOK := PublicPortSelectorForApp(app, tt.selector)
			if gotPort != tt.wantPort || gotOK != tt.wantOK {
				t.Fatalf("PublicPortSelectorForApp(%q) = (%d, %v), want (%d, %v)", tt.selector, gotPort, gotOK, tt.wantPort, tt.wantOK)
			}
		})
	}
}
