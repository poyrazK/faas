package edgedns_test

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/edgedns"
)

// TestReconcilerKeepsADNSOnlyNameOnNodesThatServeIt: a node whose edge is
// ready but whose public address stops serving the DNS-only name (a rollback
// to a release without direct entry) leaves that name after the misses and
// stays behind the proxied one, and comes back once it serves again.
func TestReconcilerKeepsADNSOnlyNameOnNodesThatServeIt(t *testing.T) {
	pilot := edgedns.Name{ZoneID: wildcard.ZoneID, Host: "pilot.gregale.dev", DNSOnly: true}
	dns := newFakeDNS(addrA, addrB)
	dns.records[pilot.Host] = []edgedns.Record{{ID: "pa", Addr: addrA, DNSOnly: true}, {ID: "pb", Addr: addrB, DNSOnly: true}}
	r, err := edgedns.NewReconciler(edgedns.Config{Names: []edgedns.Name{wildcard, pilot}}, dns, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	step := func(bDirect bool) {
		t.Helper()
		if err := r.Step(context.Background(), []edgedns.Node{
			{Name: "a", Public: addrA, Active: true, Ready: true, Direct: true},
			{Name: "b", Public: addrB, Active: true, Ready: true, Direct: bDirect},
		}); err != nil {
			t.Fatal(err)
		}
	}
	pilotAddrs := func() []netip.Addr {
		var out []netip.Addr
		for _, rec := range dns.records[pilot.Host] {
			out = append(out, rec.Addr)
		}
		slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
		return out
	}
	for range edgedns.DefaultFailAfter {
		step(false)
	}
	if got := pilotAddrs(); !slices.Equal(got, []netip.Addr{addrA}) {
		t.Fatalf("pilot records = %v, want only the node that serves it", got)
	}
	if got := dns.addrs(); !slices.Equal(got, []netip.Addr{addrA, addrB}) {
		t.Fatalf("proxied records = %v, want both ready nodes", got)
	}
	for range edgedns.DefaultRestoreAfter {
		step(true)
	}
	if got := pilotAddrs(); !slices.Equal(got, []netip.Addr{addrA, addrB}) {
		t.Fatalf("pilot records = %v after b serves again, want both", got)
	}
}

// directServer is a TLS server for the probe to dial in place of a node's
// public address, with the roots that trust it.
func directServer(t *testing.T) (dial func(context.Context, string, string) (net.Conn, error), roots *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	roots = x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	addr := srv.Listener.Addr().String()
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}, roots
}

func TestTLSProbeServes(t *testing.T) {
	dial, roots := directServer(t)
	node := netip.MustParseAddr("203.0.113.9")
	ctx := context.Background()
	// httptest's certificate names example.com.
	if !(edgedns.TLSProbe{Roots: roots, Dial: dial}).Serves(ctx, node, "example.com") {
		t.Fatal("a verified handshake for the certificate's name failed")
	}
	if (edgedns.TLSProbe{Roots: roots, Dial: dial}).Serves(ctx, node, "pilot.gregale.dev") {
		t.Fatal("a certificate that does not name the host passed")
	}
	if (edgedns.TLSProbe{Dial: dial}).Serves(ctx, node, "example.com") {
		t.Fatal("a certificate no client trusts passed")
	}
	if !(edgedns.TLSProbe{NameOnly: true, Dial: dial}).Serves(ctx, node, "example.com") {
		t.Fatal("a name-only check refused an untrusted chain that names the host")
	}
	if (edgedns.TLSProbe{NameOnly: true, Dial: dial}).Serves(ctx, node, "pilot.gregale.dev") {
		t.Fatal("a name-only check passed a certificate for another name")
	}
}

func TestTLSProbeFailsWithNothingListening(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	if (edgedns.TLSProbe{NameOnly: true, Dial: dial}).Serves(context.Background(), netip.MustParseAddr("203.0.113.9"), "example.com") {
		t.Fatal("a closed port passed")
	}
}
