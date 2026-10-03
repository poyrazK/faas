package api

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

func TestCapabilitiesForPlanProjectsCanonicalCatalog(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	response, err := CapabilitiesForPlan(PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if response.RegistryVersion != catalog.Version {
		t.Fatalf("registry version = %d, want %d", response.RegistryVersion, catalog.Version)
	}
	publicCount := 0
	for _, capability := range catalog.Capabilities {
		if capability.Maturity != productcap.MaturityInternal {
			publicCount++
		}
	}
	if len(response.Capabilities) != publicCount {
		t.Fatalf("customer capability count = %d, want %d (internal rows omitted)", len(response.Capabilities), publicCount)
	}
	for _, capability := range response.Capabilities {
		if capability.Key == "managed-postgres" {
			t.Fatal("internal capability leaked into customer response")
		}
		if capability.Key == "object-storage" && !capability.Enabled {
			t.Fatal("pro should be entitled to object storage")
		}
	}
}

func TestCapabilitiesForPlanResolvesPlanEntitlements(t *testing.T) {
	free, err := CapabilitiesForPlan(PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	hobby, err := CapabilitiesForPlan(PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	freeByKey := capabilityStatusByKey(free)
	hobbyByKey := capabilityStatusByKey(hobby)
	if !freeByKey["custom-domains"].Enabled {
		t.Fatal("custom domains should be enabled on free")
	}
	if freeByKey["object-storage"].Enabled {
		t.Fatal("object storage should be gated off on free")
	}
	if !hobbyByKey["object-storage"].Enabled {
		t.Fatal("object storage should be enabled on hobby")
	}
}

func TestCapabilitiesForPlanExplainsRestrictionsAndOmitsEnabledReasons(t *testing.T) {
	for _, plan := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale, Plan("unknown")} {
		t.Run(string(plan), func(t *testing.T) {
			response, err := CapabilitiesForPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, capability := range response.Capabilities {
				if capability.Maturity == CapabilityMaturityInternal {
					t.Fatalf("internal capability leaked: %s", capability.Key)
				}
				encoded, err := json.Marshal(capability)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(encoded, &fields); err != nil {
					t.Fatal(err)
				}
				if capability.Enabled {
					if _, ok := fields["unavailable_reason"]; ok {
						t.Errorf("enabled %s carries a reason", capability.Key)
					}
					if _, ok := fields["unavailable_detail"]; ok {
						t.Errorf("enabled %s carries a detail", capability.Key)
					}
					continue
				}
				if capability.UnavailableReason != CapabilityUnavailablePlan || capability.UnavailableDetail == "" {
					t.Errorf("%s lacks a plan explanation: %+v", capability.Key, capability)
				}
			}
			if plan == Plan("unknown") {
				for _, capability := range response.Capabilities {
					if capability.Enabled {
						t.Errorf("unknown plan enabled %s", capability.Key)
					}
				}
			}
		})
	}
}

func TestCapabilitiesResponseJSONShape(t *testing.T) {
	response, err := CapabilitiesForPlan(PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"registry_version", "plan", "capabilities"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("response missing %q", key)
		}
	}
}

func capabilityStatusByKey(response CapabilitiesResponse) map[string]CapabilityStatus {
	result := make(map[string]CapabilityStatus, len(response.Capabilities))
	for _, capability := range response.Capabilities {
		result[capability.Key] = capability
	}
	return result
}
