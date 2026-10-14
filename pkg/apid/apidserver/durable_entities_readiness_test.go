package apidserver_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"net/http/httptest"
	"testing"
)

func TestDurableEntitySocketReadinessDoesNotAdmitOrClaimAcceptance(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	path := "/v1/apps/" + app.Slug + "/entities/readiness"
	for range 2 {
		response := e.do(t, "GET", path, nil, nil)
		var report durable.DurableEntityReadiness
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &report) != nil || !report.ConfigurationReady || report.ReleaseReady || report.Scope != "serving_node" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response.Code, response.Body.String())
		}
		unverified := 0
		for _, check := range report.Checks {
			if check.Status == "unverified" {
				unverified++
			}
		}
		if unverified != 4 {
			t.Fatal("external evidence inferred", report)
		}
	}
	if dispatch.calls.Load() != 0 {
		t.Fatal("readiness dispatched a guest")
	}
	// Restore normal quota admission after inspection: the one-attempt budget
	// must still be available, and no connection lease was acquired.
	response := e.do(t, "GET", "/v1/apps/"+app.Slug+"/entities/quotas?namespace=counters&key=customer:456", nil, nil)
	var usage map[string]any
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &usage) != nil || usage["invocations"] != float64(0) || usage["connections"] != float64(0) {
		t.Fatal(response.Body.String())
	}
	e.s.Durable().TenantQuotas = nil
	response = e.do(t, "GET", path, nil, nil)
	var disabled durable.DurableEntityReadiness
	if json.Unmarshal(response.Body.Bytes(), &disabled) != nil || disabled.ConfigurationReady {
		t.Fatal("missing quotas accepted", response.Body.String())
	}
	e.s.Durable().TenantQuotas = q
	// An app outside the allowlist does not see the preview, readiness included.
	delete(e.s.Durable().Apps, app.ID)
	assertUnregisteredRoute(t, e.do(t, "GET", path, nil, nil))
	request := httptest.NewRequest("GET", path, nil)
	unauthenticated := httptest.NewRecorder()
	e.h.ServeHTTP(unauthenticated, request)
	if unauthenticated.Code == 200 {
		t.Fatal("anonymous readiness access")
	}
}
