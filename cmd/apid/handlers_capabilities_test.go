package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcap"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetCapabilitiesReturnsPlanResolvedRegistry(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	(&server{}).getCapabilities(recorder, request, state.Account{Plan: api.PlanPro})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	var response api.CapabilitiesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	if response.Plan != string(api.PlanPro) || response.RegistryVersion != catalog.Version {
		t.Fatalf("unexpected response metadata: %+v", response)
	}
	if len(response.Capabilities) == 0 {
		t.Fatal("expected customer capabilities")
	}
}

func TestGetCapabilitiesDisablesDarkLaunchedContractPreview(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	(&server{}).getCapabilities(recorder, request, state.Account{Plan: api.PlanScale})

	var response api.CapabilitiesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, capability := range response.Capabilities {
		if capability.Key == "openapi-contract-preview" {
			if capability.Enabled {
				t.Fatal("openapi-contract-preview enabled while runtime flag is off")
			}
			return
		}
	}
	t.Fatal("openapi-contract-preview capability missing")
}
