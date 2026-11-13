package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// ADR-20261110210000 slice 3i: the forwarder names a sealed rule header to
// the instance's vmmd and never sends a value for it; a set op drops the
// client's own value first.
func TestForwardingReverseProxy_NamesEdgeSecretHeaders(t *testing.T) {
	stream := noContentHTTPStream()
	proxy := gateway.ForwardingReverseProxy(&fakeNodeLookup{cli: &fakeVmmdClient{Stream: stream}}, nil)
	r := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
	r.Body = http.NoBody
	r.Header.Set("x-faas-instance", "i-test")
	r.Header.Set("Authorization", "Bearer from-the-client")
	r.Header.Set("X-Api-Key", "client-key")
	gateway.RecordEdgeSecretHeader(r, gateway.EdgeRuleHeaderOp{Name: "Authorization", Action: "set", SecretAppID: "app-a", SecretID: "s-1"})
	gateway.RecordEdgeSecretHeader(r, gateway.EdgeRuleHeaderOp{Name: "X-Api-Key", Action: "add", SecretAppID: "app-b", SecretID: "s-2"})
	rec := httptest.NewRecorder()
	proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test"}).ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	init := stream.Sends[0].GetInit()
	var refs []string
	for _, ref := range init.GetEdgeSecretHeaders() {
		refs = append(refs, ref.GetName()+"/"+ref.GetAction()+"/"+ref.GetSecretAppId()+"/"+ref.GetSecretId())
	}
	if want := "Authorization/set/app-a/s-1,X-Api-Key/add/app-b/s-2"; strings.Join(refs, ",") != want {
		t.Fatalf("edge secret headers = %v, want %s", refs, want)
	}
	keys := 0
	for _, h := range init.GetHeaders() {
		switch {
		case strings.EqualFold(h.GetName(), "Authorization"):
			t.Fatalf("the client's Authorization crossed a set rule: %q", h.GetValue())
		case strings.EqualFold(h.GetName(), "X-Api-Key"):
			keys++
		}
	}
	if keys != 1 {
		t.Fatalf("X-Api-Key lines = %d, want the client's one (vmmd adds the rule's)", keys)
	}
}
