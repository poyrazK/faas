package gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// adr: 20261026100000 — each apps domain has its own Cloudflare zone and
// Worker, so a Worker subrequest from any owned zone gets the 504 transport.
func TestIsEdgeWorkerZoneAcceptsEveryOwnedZone(t *testing.T) {
	d, err := hostidentity.NewDomains("gregale.dev", "gregale.app", "gregale.app", []string{"old.example"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hostidentity.Configure(d))
	for zone, want := range map[string]bool{
		"gregale.dev":   true,
		" Gregale.App ": true,
		"old.example":   true,
		"evil.example":  false,
		"":              false,
	} {
		if got := isEdgeWorkerZone(zone); got != want {
			t.Errorf("isEdgeWorkerZone(%q) = %v, want %v", zone, got, want)
		}
	}
}
