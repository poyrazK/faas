package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestScenarioPreflightChecksCapacityAndRequiredFeatures(t *testing.T) {
	scenario := testScenario{
		Project: "export-api", Consumers: []testConsumer{{Name: "customer-a"}, {Name: "customer-b"}},
		Services: map[string]testService{
			"worker": {AsyncRoutes: []testAsyncRoute{{Path: "/process", Methods: []string{"POST"}}}},
			"sink":   {},
		},
		Buckets: []testBucket{{Name: "exports"}},
	}
	account := api.AccountResponse{Plan: "pro", Limits: api.AccountLimits{DeveloperApps: 5}, DeveloperAppCount: 1}
	capabilities := api.CapabilitiesResponse{Capabilities: []api.CapabilityStatus{{Key: "object-storage", Enabled: true}}}
	result := assessTestPreflight("export", scenario, []string{"warm", "cold", "restored"}, 2, account, capabilities)
	if !result.Ready || result.WorkloadsPerRun != 3 || result.EstimatedDeploys != 18 || result.MaxWorkloadMinutes != 270 {
		t.Fatalf("pro preflight = %+v", result)
	}
	if got := estimateTestWorkloadMinutes(testScenario{Services: scenario.Services, Timeout: "30s"}, 2); got != 3 {
		t.Fatalf("sub-minute cost ceiling = %d, want 3", got)
	}
	account.DeveloperAppCount = 3
	result = assessTestPreflight("export", scenario, []string{"restored"}, 1, account, capabilities)
	if result.Ready {
		t.Fatalf("insufficient developer slots accepted: %+v", result)
	}
	account.DeveloperAppCount = 0
	capabilities.Capabilities[0].Enabled = false
	result = assessTestPreflight("export", scenario, []string{"restored"}, 1, account, capabilities)
	if result.Ready {
		t.Fatalf("missing object storage accepted: %+v", result)
	}
}
