package routerequirements

// ADR-448: complete captured coverage bound to saved app intent.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomaticRouteCheckResultLimitCannotRetainPartialPass(t *testing.T) {
	check := api.RouteRequirementsCheck{Version: 1, AppID: "app-id", DeploymentID: "deployment", RequirementsRevision: 3, RequirementsSHA256: strings.Repeat("a", 64), ConfigurationSHA256: strings.Repeat("b", 64), Report: api.RouteRequirementsReport{Version: 2, Status: "satisfied", Scope: strings.Repeat("x", api.RouteCheckMaxResultBytes), Routes: []api.RouteRequirementsResult{{Method: "GET", Path: "/private", Status: "satisfied"}}}}
	bounded, err := BoundAutomaticCheck(check)
	body, marshalErr := json.Marshal(bounded)
	if err != nil || marshalErr != nil || len(body) > api.RouteCheckMaxResultBytes || bounded.Report.Status != "unknown" || bounded.Report.Coverage.Code != "automatic_result_limit" || len(bounded.Report.Routes) != 0 || bounded.RequirementsRevision != 3 || bounded.RequirementsSHA256 != check.RequirementsSHA256 || bounded.ConfigurationSHA256 != check.ConfigurationSHA256 {
		t.Fatalf("oversized automatic result retained partial evidence: %+v %v", bounded, err)
	}
}

func TestSavedRouteCheckCoverageAndConfigurationChanges(t *testing.T) {
	for _, scenario := range []string{"satisfied", "uncovered endpoint", "weakened consumer auth", "budget drift", "missing capture", "unknown policy", "invalid digest", "foreign app"} {
		t.Run(scenario, func(t *testing.T) {
			config := coverageConfig(t, `{"version":2,"groups":[{"name":"checkout","path_prefix":"/checkout/","methods":["POST"],"require":{"authentication":"consumer","budget":{"max_ms":3000}}}]}`)
			config, digest, _ := NormalizeCoverage(config)
			context := requirementContext()
			context.App.ConsumerAuthMode = "required"
			inventory := groupInventory(CapturedRoute{Method: "POST", Path: "/checkout/{id}"})
			saved := api.SavedRouteRequirements{AppID: context.App.ID, Revision: 1, SHA256: digest, Requirements: config}
			want := "satisfied"
			switch scenario {
			case "uncovered endpoint":
				inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "GET", Path: "/export"})
				want = "violated"
			case "weakened consumer auth":
				context.App.ConsumerAuthMode = "optional"
				want = "violated"
			case "budget drift":
				context.App.RequestTimeoutS = 4
				want = "violated"
			case "missing capture":
				inventory.Status, inventory.Code = "unavailable", "candidate_contract_truncated"
				want = "unknown"
			case "unknown policy":
				context.Rules = []api.EdgeRuleResponse{requirementRule("conditional", "budget", `{"budget":{"budget_ms":2000}}`, 0)}
				context.Rules[0].MatchPath = "/checkout/*"
				context.Rules[0].MatchHeaders = map[string]string{"X-Private": "secret"}
				want = "unknown"
			case "invalid digest":
				saved.SHA256 = "invalid"
			case "foreign app":
				saved.AppID = "another-app"
			}
			result, err := BuildSavedCheck(saved, context, "pro", inventory.Deployment, inventory)
			if scenario == "invalid digest" || scenario == "foreign app" {
				if err == nil {
					t.Fatal("unbound intent accepted")
				}
				return
			}
			if err != nil || result.Report.Status != want || result.RequirementsRevision != 1 || result.RequirementsSHA256 != digest || result.Report.SHA256 != digest || result.Report.Coverage == nil {
				t.Fatalf("check: %+v %v", result, err)
			}
			original := result.ConfigurationSHA256
			context.App.ConsumerAuthMode = "optional"
			context.App.RequestTimeoutS++
			changed, _ := BuildSavedCheck(saved, context, "pro", inventory.Deployment, inventory)
			if changed.ConfigurationSHA256 == original || changed.RequirementsSHA256 != digest {
				t.Fatal("policy edit did not change configuration provenance")
			}
		})
	}
}
