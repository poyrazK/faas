package apidserver_test

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r58 (rc.381): `egress-allowlist add 10.0.0.0/8` was saved and
// reported as updated, though apps can never reach a private range; and
// `add 0.0.0.0/0` said "parse failed". A new entry inside the platform
// denylist is refused; an app that already holds one can still change its
// list.
func TestEgressAllowlistRefusesBlockedRanges(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	held := netip.MustParsePrefix("192.168.0.0/16")
	if _, err := store.CreateApp(t.Context(), state.App{
		AccountID: e.Acct().ID, Slug: "egress-private", Type: state.AppTypeFunction,
		Runtime: "node22", RAMMB: 256, MaxConcurrency: 1, CreatedAt: time.Now(),
		EgressAllowlist: []netip.Prefix{held},
	}); err != nil {
		t.Fatal(err)
	}
	patch := func(entries ...string) (int, string) {
		rec := e.Do(t, http.MethodPatch, "/v1/apps/egress-private", map[string]any{"egress_allowlist": entries}, nil)
		return rec.Code, rec.Body.String()
	}
	for _, entry := range []string{"10.0.0.0/8", "10.1.2.0/24", "169.254.169.254/32", "fd00::/48"} {
		code, body := patch(held.String(), entry)
		if code != http.StatusBadRequest || !strings.Contains(body, "apps can never reach") {
			t.Fatalf("allowlist %s: %d %s, want 400 naming the blocked range", entry, code, body)
		}
	}
	if code, body := patch("0.0.0.0/0"); code != http.StatusBadRequest || !strings.Contains(body, "egress-allowlist clear") || strings.Contains(body, "parse failed") {
		t.Fatalf("allowlist /0: %d %s, want 400 pointing at clear", code, body)
	}
	// A range only partly blocked keeps its public part; the held private
	// entry does not stop the app changing its list.
	if code, body := patch(held.String(), "100.0.0.0/8", "203.0.113.0/24"); code != http.StatusOK {
		t.Fatalf("held entry plus public ranges: %d %s, want 200", code, body)
	}
}
