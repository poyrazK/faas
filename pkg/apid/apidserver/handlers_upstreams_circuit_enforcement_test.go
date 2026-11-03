// adr: 570 — an egress circuit breaker the platform does not enforce is refused.
package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestUpstreamCircuitBreakerOptInNeedsPlatformEnforcement(t *testing.T) {
	enabled, disabled := true, false
	for _, tc := range []struct {
		name     string
		enforced bool
		body     api.UpdateUpstreamCircuitBreakerRequest
		refused  bool
	}{
		{"opt in while unenforced", false, api.UpdateUpstreamCircuitBreakerRequest{Enabled: &enabled}, true},
		{"opt out while unenforced", false, api.UpdateUpstreamCircuitBreakerRequest{Enabled: &disabled}, false},
		{"opt in while enforced", true, api.UpdateUpstreamCircuitBreakerRequest{Enabled: &enabled}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			(*e.S()).WithDataPlacement(true).WithEgressCircuitEnforcement(tc.enforced)
			app := apidserver.CreateApp(t, e, "circuit-"+strings.ReplaceAll(tc.name, " ", "-"))
			rec := e.Do(t, http.MethodPatch, "/v1/apps/"+app.Slug+"/upstreams/"+uuid.NewString()+"/circuit-breaker", tc.body, nil)
			refused := rec.Code == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), api.CodeEgressCircuitDisabled)
			if refused != tc.refused {
				t.Fatalf("status=%d body=%s, want refused=%v", rec.Code, rec.Body, tc.refused)
			}
			// Requests the gate admits continue to the (here missing) upstream.
			if !tc.refused && rec.Code != http.StatusNotFound {
				t.Fatalf("admitted request status=%d body=%s, want the upstream lookup's 404", rec.Code, rec.Body)
			}
		})
	}
}
