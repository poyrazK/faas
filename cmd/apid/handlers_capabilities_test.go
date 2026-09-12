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

func capabilityByKey(t *testing.T, response api.CapabilitiesResponse, key string) api.CapabilityStatus {
	t.Helper()
	for _, capability := range response.Capabilities {
		if capability.Key == key {
			return capability
		}
	}
	t.Fatalf("capability %q missing", key)
	return api.CapabilityStatus{}
}

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
	if got := capabilityByKey(t, response, "disposable-runs"); got.Enabled {
		t.Fatalf("disposable runs advertised enabled without its runtime: %+v", got)
	}
	if got := capabilityByKey(t, response, "object-storage"); got.Enabled {
		t.Fatalf("object storage advertised enabled without provider configuration: %+v", got)
	}
}

func TestGetCapabilitiesRequiresEntitlementAndRuntimeAvailability(t *testing.T) {
	s := (&server{}).WithExecutionAPIEnabled(true)
	for _, test := range []struct {
		name    string
		plan    api.Plan
		enabled bool
	}{
		{name: "paid and available", plan: api.PlanPro, enabled: true},
		{name: "free remains unavailable", plan: api.PlanFree, enabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			s.getCapabilities(recorder, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil), state.Account{Plan: test.plan})
			var response api.CapabilitiesResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if got := capabilityByKey(t, response, "disposable-runs").Enabled; got != test.enabled {
				t.Fatalf("disposable-runs enabled = %v, want %v", got, test.enabled)
			}
		})
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

func TestGetCapabilitiesDisablesDisposableRunsWhenRuntimeIsUnavailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	(&server{}).getCapabilities(recorder, request, state.Account{Plan: api.PlanPro})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response api.CapabilitiesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, capability := range response.Capabilities {
		if capability.Key == disposableRunsCapabilityKey {
			if capability.Enabled {
				t.Fatal("disposable runs advertised while execution API is disabled")
			}
			return
		}
	}
	t.Fatalf("capability %q missing from response", disposableRunsCapabilityKey)
}

func TestGetCapabilitiesEnablesDisposableRunsWhenRuntimeIsAvailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	server := (&server{}).WithExecutionAPIEnabled(true)
	server.getCapabilities(recorder, request, state.Account{Plan: api.PlanPro})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response api.CapabilitiesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, capability := range response.Capabilities {
		if capability.Key == disposableRunsCapabilityKey {
			if !capability.Enabled {
				t.Fatal("disposable runs not advertised when execution API is enabled for entitled plan")
			}
			return
		}
	}
	t.Fatalf("capability %q missing from response", disposableRunsCapabilityKey)
}

func TestDisposableRunCapabilityMatchesAdmissionGate(t *testing.T) {
	for _, tc := range []struct {
		name             string
		executionEnabled bool
		wantStatus       int
		wantCapability   bool
	}{
		{name: "runtime unavailable", wantStatus: http.StatusNotImplemented},
		{name: "runtime available", executionEnabled: true, wantStatus: http.StatusAccepted, wantCapability: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, api.PlanHobby)
			if tc.executionEnabled {
				enableExecutionAPIForTest(t, &e)
			}

			capabilityRecorder := httptest.NewRecorder()
			e.s.getCapabilities(capabilityRecorder, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil), e.acct)
			if capabilityRecorder.Code != http.StatusOK {
				t.Fatalf("capabilities status = %d, want 200", capabilityRecorder.Code)
			}
			var capabilities api.CapabilitiesResponse
			if err := json.Unmarshal(capabilityRecorder.Body.Bytes(), &capabilities); err != nil {
				t.Fatal(err)
			}
			var advertised bool
			found := false
			for _, capability := range capabilities.Capabilities {
				if capability.Key == disposableRunsCapabilityKey {
					advertised = capability.Enabled
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("capability %q missing from response", disposableRunsCapabilityKey)
			}

			executionRecorder := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
			if executionRecorder.Code != tc.wantStatus {
				t.Fatalf("execution status = %d, want %d; body=%s", executionRecorder.Code, tc.wantStatus, executionRecorder.Body.String())
			}
			if advertised != tc.wantCapability {
				t.Fatalf("disposable-runs advertised = %v, want %v", advertised, tc.wantCapability)
			}
		})
	}
}
