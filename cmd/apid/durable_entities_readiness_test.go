package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestDurableEntitySocketReadinessDoesNotAdmitOrClaimAcceptance(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	path := "/v1/apps/" + app.Slug + "/entities/readiness"
	for range 2 {
		response := e.do(t, "GET", path, nil, nil)
		var report durableEntityReadiness
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
	e.s.durableEntityTenantQuotas = nil
	response = e.do(t, "GET", path, nil, nil)
	var disabled durableEntityReadiness
	if json.Unmarshal(response.Body.Bytes(), &disabled) != nil || disabled.ConfigurationReady {
		t.Fatal("missing quotas accepted", response.Body.String())
	}
	e.s.durableEntityTenantQuotas = q
	delete(e.s.durableEntityApps, app.ID)
	response = e.do(t, "GET", path, nil, nil)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &disabled) != nil || disabled.ConfigurationReady {
		t.Fatal("disabled app cannot inspect blockers", response.Body.String())
	}
	request := httptest.NewRequest("GET", path, nil)
	unauthenticated := httptest.NewRecorder()
	e.h.ServeHTTP(unauthenticated, request)
	if unauthenticated.Code == 200 {
		t.Fatal("anonymous readiness access")
	}
}
