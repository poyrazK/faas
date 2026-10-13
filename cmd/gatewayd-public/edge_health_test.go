// adr: 1152
// edge_health_test.go pins the Cloudflare Load Balancer signal (ADR-1152 §2):
// /edgez follows the compute gateway check and draining, and stays ready
// through a Postgres outage that fails /readyz.

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func edgez(t *testing.T, e *edgeHealth) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	e.handler()(rec, httptest.NewRequest(http.MethodGet, "/edgez", nil))
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	return rec.Code, rec.Body.String()
}

// adr: 1152
func TestEdgeHealthIgnoresPostgresButFollowsGatewayAndDrain(t *testing.T) {
	probe := &gateway.ReadyzProbe{}
	pgSig := probe.Register()
	internalSig := probe.Register()
	e := &edgeHealth{internal: internalSig}

	pgSig.Set(false, "postgres unreachable")
	internalSig.Set(true, "")
	if ok, _ := probe.All(); ok {
		t.Fatal("fixture: /readyz should be failing during the Postgres outage")
	}
	if code, body := edgez(t, e); code != http.StatusOK {
		t.Fatalf("edgez during a Postgres outage = %d %q, want 200", code, body)
	}

	internalSig.Set(false, "gatewayd-internal unreachable")
	if code, body := edgez(t, e); code != http.StatusServiceUnavailable || !strings.Contains(body, "unreachable") {
		t.Fatalf("edgez without a compute gateway = %d %q, want 503", code, body)
	}

	internalSig.Set(true, "")
	e.startDraining()
	if code, body := edgez(t, e); code != http.StatusServiceUnavailable || !strings.Contains(body, "draining") {
		t.Fatalf("edgez while draining = %d %q, want 503 draining", code, body)
	}
}
