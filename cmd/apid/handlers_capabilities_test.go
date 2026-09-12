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
