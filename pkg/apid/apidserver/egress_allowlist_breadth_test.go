package apidserver_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Allowlisted destinations skip the SMTP block, the port policy and the DNS
// gate, so 0.0.0.0/1 and 128.0.0.0/1 lifted every abuse control for the
// whole internet (bug hunt 2026-10-28 H51).
func TestEgressAllowlistRefusesBroadPrefixes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	if _, err := store.CreateApp(t.Context(), state.App{
		AccountID: e.Acct().ID, Slug: "egress-broad", Type: state.AppTypeFunction,
		Runtime: "node22", RAMMB: 256, MaxConcurrency: 1, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"0.0.0.0/1", "128.0.0.0/7", "2000::/3"} {
		rec := e.Do(t, http.MethodPatch, "/v1/apps/egress-broad", map[string]any{"egress_allowlist": []string{entry}}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("allowlist %s: status %d %s, want 400", entry, rec.Code, rec.Body.String())
		}
	}
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/egress-broad", map[string]any{"egress_allowlist": []string{"203.0.113.0/24"}}, nil); rec.Code != http.StatusOK {
		t.Fatalf("narrow allowlist: status %d %s, want 200", rec.Code, rec.Body.String())
	}
}
