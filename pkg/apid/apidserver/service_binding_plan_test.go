package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestServiceBindingPlanAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	created := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "service-plan-api"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d %s", created.Code, created.Body)
	}
	read := func() api.AppResponse {
		t.Helper()
		rec := e.Do(t, http.MethodGet, "/v1/apps/service-plan-api", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get %d %s", rec.Code, rec.Body)
		}
		var app api.AppResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
			t.Fatal(err)
		}
		return app
	}
	before := read()
	if len(before.ServiceBindingFingerprint) != 64 {
		t.Fatalf("fingerprint=%q", before.ServiceBindingFingerprint)
	}
	https := api.ServiceBindingTransportHTTPS
	request := api.UpdateAppRequest{ExpectedServiceBindingFingerprint: &before.ServiceBindingFingerprint, ServiceBindingTransport: &https}
	rec := e.Do(t, http.MethodPatch, "/v1/apps/service-plan-api", request, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("guarded patch %d %s", rec.Code, rec.Body)
	}
	saved := read()
	if saved.ServiceBindingTransport != https || saved.ServiceBindingFingerprint == before.ServiceBindingFingerprint {
		t.Fatal("guarded update not applied")
	}
	rec = e.Do(t, http.MethodPatch, "/v1/apps/service-plan-api", request, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale patch %d %s", rec.Code, rec.Body)
	}
	if read().ServiceBindingFingerprint != saved.ServiceBindingFingerprint {
		t.Fatal("stale update mutated state")
	}
	ram := 256
	request.ExpectedServiceBindingFingerprint = &saved.ServiceBindingFingerprint
	request.RAMMB = &ram
	rec = e.Do(t, http.MethodPatch, "/v1/apps/service-plan-api", request, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed patch %d %s", rec.Code, rec.Body)
	}
	if read().RAMMB != saved.RAMMB {
		t.Fatal("rejected mixed update changed RAM")
	}
	// Clearing caller policy with explicit null remains a guarded operation.
	request = api.UpdateAppRequest{ExpectedServiceBindingFingerprint: &saved.ServiceBindingFingerprint, AllowedServiceCallers: json.RawMessage(`null`)}
	rec = e.Do(t, http.MethodPatch, "/v1/apps/service-plan-api", request, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("null reset %d %s", rec.Code, rec.Body)
	}
}
