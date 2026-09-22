// normal_path_public_edge_e2e_test.go — the gatewayd-public -> gatewayd-internal
// handover (ADR-070).
//
// Production has no path that reaches gatewayd-internal from outside. Every
// customer request lands on gatewayd-public, which hands it to
// gatewayd-internal over a unix socket. Until this file existed, no e2e test
// that runs booted gatewayd-public at all: the only one that did is
// tcp_ingress_metal_test.go, which is metal-tagged and has never executed.
// Both halves had unit coverage; the pair had none.
//
// That gap has already shipped a customer-visible bug. PR #1284: gatewayd-public
// stamped its own 3s parent budget on every request, which silently capped every
// `kind=budget` edge rule — whichever hop stamped first won. Unit tests on both
// sides passed, because neither side is wrong alone.
//
// These tests pin the handover itself: what the guest receives must not depend
// on which hop the request entered through, and the public hop must not leak
// its own transport details into the customer's request or response.

package e2e_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// guestRequestVia sends one request to the given base URL and returns the init
// frame the guest actually saw, so two entry points can be compared directly.
func guestRequestVia(t *testing.T, f *normalPathFixture, baseURL, path string, headers map[string]string, body string) *vmmdpb.ForwardHTTPRequestInit {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	method := http.MethodGet
	if body != "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = f.host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := f.h.HTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request via %s: %v", baseURL, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request via %s: status=%d", baseURL, resp.StatusCode)
	}
	init := f.vmmd.LastRequest()
	if init == nil {
		t.Fatalf("request via %s never reached the bridge", baseURL)
	}
	return init
}

// TestE2E_NormalPath_PublicEdgeHandoverPreservesRequest is the differential the
// two-hop gap needs: the same request sent through gatewayd-public and through
// gatewayd-internal must arrive at the guest the same way.
//
// Comparing the two entry points rather than asserting a fixed shape is what
// makes this robust. A future change to what the gateway forwards stays valid
// as long as it applies to both hops; only a divergence introduced by the
// handover fails, and a divergence is exactly the bug class.
func TestE2E_NormalPath_PublicEdgeHandoverPreservesRequest(t *testing.T) {
	f := newNormalPathFixture(t, "public-edge-parity")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")

	if f.h.GatewayPublicURL == "" {
		t.Fatal("gatewayd-public was not booted; the handover is untested")
	}
	if f.h.EdgeURL() != f.h.GatewayPublicURL {
		t.Fatalf("EdgeURL()=%q, want the public listener %q", f.h.EdgeURL(), f.h.GatewayPublicURL)
	}

	headers := map[string]string{
		"X-Customer-Header": "keep-me",
		"Content-Type":      "application/json",
	}
	const body = `{"hello":"world"}`

	viaPublic := guestRequestVia(t, f, f.h.GatewayPublicURL, "/handover", headers, body)
	viaInternal := guestRequestVia(t, f, f.h.GatewayURL, "/handover", headers, body)

	if viaPublic.GetMethod() != viaInternal.GetMethod() {
		t.Errorf("method differs across hops: public=%q internal=%q",
			viaPublic.GetMethod(), viaInternal.GetMethod())
	}
	if viaPublic.GetRequestUri() != viaInternal.GetRequestUri() {
		t.Errorf("request-target differs across hops: public=%q internal=%q",
			viaPublic.GetRequestUri(), viaInternal.GetRequestUri())
	}

	// The customer's own headers must survive the extra hop untouched.
	for name, want := range headers {
		if !hasNormalPathHeader(viaPublic, name, want) {
			t.Errorf("header %s=%q did not survive the public hop", name, want)
		}
	}

	// Any header the guest sees through one hop and not the other is a
	// handover divergence. Forwarding headers that the public hop legitimately
	// adds are allowed; silently dropping something internal forwards is not.
	internalOnly := headerNamesMissingFrom(viaInternal, viaPublic)
	if len(internalOnly) > 0 {
		t.Errorf("the public hop dropped headers gatewayd-internal forwards: %v", internalOnly)
	}
}

// TestE2E_NormalPath_PublicEdgeDoesNotLeakTransportHeaders pins the other half:
// the public hop must not expose its own plumbing to the guest. The handover is
// a unix socket inside one host, and a guest that can see hop-local transport
// headers can be confused by them — or worse, can have a customer-supplied
// value silently overwritten by one.
func TestE2E_NormalPath_PublicEdgeDoesNotLeakTransportHeaders(t *testing.T) {
	f := newNormalPathFixture(t, "public-edge-leak")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")

	init := guestRequestVia(t, f, f.h.EdgeURL(), "/leak-check", nil, "")

	// Hop-by-hop headers are per-connection by definition (RFC 9110 §7.6.1)
	// and must never be forwarded across either hop.
	for _, name := range []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Connection",
		"Transfer-Encoding",
		"Upgrade",
	} {
		if hasNormalPathHeaderName(init, name) {
			t.Errorf("hop-by-hop header %q reached the guest through the public edge", name)
		}
	}
}

// headerNamesMissingFrom returns the header names present on want but absent
// from got, lower-cased for a case-insensitive comparison.
func headerNamesMissingFrom(want, got *vmmdpb.ForwardHTTPRequestInit) []string {
	present := make(map[string]struct{})
	for _, h := range got.GetHeaders() {
		present[strings.ToLower(h.GetName())] = struct{}{}
	}
	var missing []string
	for _, h := range want.GetHeaders() {
		name := strings.ToLower(h.GetName())
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}
