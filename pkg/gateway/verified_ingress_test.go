// adr: 20261102081618
// verified_ingress_test.go pins the trust a client certificate grants: a
// compute edge's TLS listener trusts Cloudflare's forwarded client address
// only when the connection is marked and its certificate was verified.
package gateway_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestVerifiedIngressTrustsForwardedClient(t *testing.T) {
	loopback := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	verified := &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	for _, tc := range []struct {
		name       string
		remote     string
		marked     bool
		tls        *tls.ConnectionState
		wantTrust  bool
		wantClient string
		wantProto  string
	}{
		{name: "verified certificate on a marked connection", remote: "172.70.1.1:5000", marked: true, tls: verified, wantTrust: true, wantClient: "203.0.113.9", wantProto: "https"},
		{name: "marked connection without a verified certificate", remote: "172.70.1.1:5000", marked: true, tls: &tls.ConnectionState{}, wantClient: "172.70.1.1", wantProto: "https"},
		{name: "verified certificate without the mark", remote: "172.70.1.1:5000", tls: verified, wantClient: "172.70.1.1", wantProto: "https"},
		{name: "plain request on a marked connection", remote: "172.70.1.1:5000", marked: true, wantClient: "172.70.1.1", wantProto: "http"},
		{name: "loopback connector still trusted by address", remote: "127.0.0.1:5000", wantTrust: true, wantClient: "203.0.113.9", wantProto: "http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://shop.gregale.dev/", nil)
			r.RemoteAddr = tc.remote
			r.TLS = tc.tls
			r.Header.Set("X-Forwarded-For", "203.0.113.9")
			if tc.marked {
				r = r.WithContext(gateway.WithVerifiedIngress(context.Background()))
			}
			if got := gateway.TrustedIngressPeer(r, loopback); got != tc.wantTrust {
				t.Fatalf("TrustedIngressPeer = %v, want %v", got, tc.wantTrust)
			}
			client, proto := gateway.CanonicalForwardingContext(r, loopback)
			if client != tc.wantClient || proto != tc.wantProto {
				t.Fatalf("CanonicalForwardingContext = %q %q, want %q %q", client, proto, tc.wantClient, tc.wantProto)
			}
		})
	}
}
