package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGetCapabilitiesExplainsPlanAndRuntimeGates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plan    api.Plan
		runtime bool
	}{
		{name: "free runtime off", plan: api.PlanFree},
		{name: "free runtime on", plan: api.PlanFree, runtime: true},
		{name: "pro runtime off", plan: api.PlanPro},
		{name: "pro runtime on", plan: api.PlanPro, runtime: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "")
			if tc.runtime {
				t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
			}
			e := setup(t, tc.plan)
			setS3Flag(t, e, tc.runtime)
			s := e.s.WithExecutionAPIEnabled(tc.runtime).
				WithGitHubDeploysAvailable(func(context.Context) bool { return tc.runtime })
			if tc.runtime {
				s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
			}
			recorder := httptest.NewRecorder()
			s.getCapabilities(recorder, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil), state.Account{Plan: tc.plan})
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
			}
			var response api.CapabilitiesResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"openapi-contract-preview", "disposable-runs", "object-storage", "github-deploys"} {
				capability := capabilityByKey(t, response, key)
				wantReason := ""
				if tc.plan == api.PlanFree && (key == "disposable-runs" || key == "object-storage") {
					wantReason = api.CapabilityUnavailablePlan
				} else if !tc.runtime {
					wantReason = api.CapabilityUnavailableRuntime
				}
				if capability.Enabled != (wantReason == "") || capability.UnavailableReason != wantReason {
					t.Errorf("%s: enabled=%t reason=%q, want reason=%q", key, capability.Enabled, capability.UnavailableReason, wantReason)
				}
				if (capability.UnavailableDetail == "") != capability.Enabled {
					t.Errorf("%s explanation does not match availability: %+v", key, capability)
				}
				if strings.Contains(capability.UnavailableDetail, "FAAS_") || strings.Contains(capability.UnavailableDetail, "external") {
					t.Errorf("%s exposes operator configuration: %q", key, capability.UnavailableDetail)
				}
			}
		})
	}
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
	if got := capabilityByKey(t, response, "github-deploys"); got.Enabled {
		t.Fatalf("GitHub deploys advertised without a healthy githubd runtime: %+v", got)
	}
}

func TestGetCapabilitiesGatesGitHubDeploysOnRuntimeReadiness(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready bool
	}{
		{name: "ready", ready: true},
		{name: "not ready", ready: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := (&server{}).WithGitHubDeploysAvailable(func(context.Context) bool { return tc.ready })
			recorder := httptest.NewRecorder()
			s.getCapabilities(recorder, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil), state.Account{Plan: api.PlanPro})
			var response api.CapabilitiesResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"github-deploys", "pr-previews"} {
				if got := capabilityByKey(t, response, key).Enabled; got != tc.ready {
					t.Fatalf("%s enabled = %t, want %t", key, got, tc.ready)
				}
			}
		})
	}
}

// production-us hunt #8: custom domains were advertised while ADR-520
// on-demand TLS was off, so no customer hostname could get a certificate.
func TestGetCapabilitiesGatesCustomDomainsOnOnDemandTLS(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{mode: "", want: false},
		{mode: api.CustomDomainTLSModeOnDemand, want: true},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", tc.mode)
			recorder := httptest.NewRecorder()
			(&server{}).getCapabilities(recorder, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil), state.Account{Plan: api.PlanPro})
			var response api.CapabilitiesResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if got := capabilityByKey(t, response, "custom-domains").Enabled; got != tc.want {
				t.Fatalf("custom-domains enabled = %t, want %t", got, tc.want)
			}
		})
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

type capabilitiesLegacyStore struct{ state.Store }

func TestGetCapabilitiesConditionalParkingBackend(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store state.Store
		want  bool
	}{
		{name: "unconfigured"},
		{name: "legacy backend", store: &capabilitiesLegacyStore{state.NewMemStore()}},
		{name: "atomic backend", store: state.NewMemStore(), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{store: tc.store}
			recorder := httptest.NewRecorder()
			s.getCapabilities(recorder, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil), state.Account{Plan: api.PlanFree})
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if got, exists := body["conditional_parking"]; !exists || got != tc.want {
				t.Fatalf("conditional parking=%v exists=%v want=%v", got, exists, tc.want)
			}
		})
	}
}
