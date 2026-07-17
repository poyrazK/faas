package oci

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
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

func TestEgressDialContext_RefusesRFC1918(t *testing.T) {
	dial := EgressDialContext(&net.Dialer{})
	// Resolve "localhost" → 127.0.0.1 and verify the dial is refused.
	conn, err := dial(context.Background(), "tcp", "localhost:80")
	if err == nil {
		_ = conn.Close()
		t.Fatal("egress dial to localhost should be denied")
	}
}

func TestEgressDialContext_RefusesMetadataIP(t *testing.T) {
	dial := EgressDialContext(&net.Dialer{})
	_, err := dial(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("egress dial to 169.254.169.254 should be denied")
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
