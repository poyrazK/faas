package state

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLifecycleReceiptValidity(t *testing.T) {
	at := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	old := `{"openapi":"3.1.0","paths":{"/old":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://app.gregale.dev/previous"}}}}`
	next := strings.ReplaceAll(old, "/previous", "/new")
	before := &RoutePolicyContract{DeploymentID: "before", Doc: []byte(old), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(old)))}
	after := &RoutePolicyContract{DeploymentID: "after", Doc: []byte(next), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(next)))}
	binding := lifecycleApprovalBinding{GateRevision: 1, RequirementsRevision: 2, RemovalPolicyRevision: 3, ConfigurationSHA256: strings.Repeat("a", 64)}
	request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &binding.GateRevision, ExpectedRequirementsRevision: &binding.RequirementsRevision, ExpectedRemovalPolicyRevision: &binding.RemovalPolicyRevision, ConfigurationSHA256: binding.ConfigurationSHA256, BaselineDeploymentID: "before", CandidateDeploymentID: "after", BaselineContractSHA256: before.SHA256, CandidateContractSHA256: after.SHA256, Mappings: []api.RouteLifecycleMapping{{Method: "GET", Path: "/old", SuccessorMethod: "GET", SuccessorPath: "/new", SuccessorURL: "https://app.gregale.dev/new"}}}
	receipt, err := lifecycleApproval(before, after, request, binding, "owner:test", "app", at, func(RoutePolicySnapshot, *RoutePolicyContract, *RoutePolicyContract, []api.RouteLifecycleMapping) error {
		return nil
	}, RoutePolicySnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ValidUntil.Sub(receipt.ApprovedAt) != api.RouteLifecycleApprovalTTL {
		t.Fatal("expiry")
	}
	for _, scenario := range []string{"valid", "expired", "future", "gate", "requirements", "removal_policy", "configuration", "mapping", "invalidated", "checker", "truncated", "earlier_sunset", "metadata_removed", "premature_removal"} {
		t.Run(scenario, func(t *testing.T) {
			a := copyLifecycleApproval(receipt)
			b := binding
			c := *after
			clock := at
			switch scenario {
			case "expired":
				clock = a.ValidUntil
			case "future":
				clock = at.Add(-time.Second)
			case "gate":
				b.GateRevision++
			case "requirements":
				b.RequirementsRevision++
			case "removal_policy":
				b.RemovalPolicyRevision++
			case "configuration":
				b.ConfigurationSHA256 = strings.Repeat("b", 64)
			case "mapping":
				a.Mappings[0].SuccessorURL = "https://app.gregale.dev/forged"
			case "invalidated":
				a.InvalidatedAt = &at
			case "checker":
				a.CheckerVersion++
			case "truncated":
				c.Truncated = true
			case "earlier_sunset":
				c.Doc = []byte(strings.ReplaceAll(next, "2099-01-01T00:00:00Z", "2026-11-01T00:00:00Z"))
			case "metadata_removed":
				c.Doc = []byte(`{"openapi":"3.1.0","paths":{"/old":{"get":{}}}}`)
			case "premature_removal":
				c.Doc = []byte(`{"openapi":"3.1.0","paths":{}}`)
			}
			valid := matchingLifecycleApproval(a, b, before, &c, clock)
			if valid != (scenario == "valid") {
				t.Fatalf("valid=%v", valid)
			}
			decision := api.RouteGateDecision{Mode: "enforce", Reasons: []string{}}
			appendLifecycleGateFindings(&decision, before, &c, clock, valid)
			if valid && len(decision.Reasons) != 0 {
				t.Fatalf("review not cleared %+v", decision)
			}
			if !valid && len(decision.Reasons) == 0 {
				t.Fatal("invalid receipt waived lifecycle finding")
			}
		})
	}
}
