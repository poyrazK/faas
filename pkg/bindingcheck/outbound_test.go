// adr: 430 — unsupported waivers never hide failed supported outbound probes.
package bindingcheck

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestEvaluateConfiguredOutboundRequiresCurrentEvidence(t *testing.T) {
	inventory, policy, now := fixture()
	credential := true
	stamp := now.Add(-DefaultMaxVerificationAge / 2)
	item := api.AppBindingInventoryItem{Type: api.BindingTypeOutbound, Name: "provider", Scope: "app", State: "enabled", CredentialConfigured: &credential, OutboundProbe: &api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}, VerificationStatus: "passed", Verification: &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: inventory.VerificationDeploymentID, Scope: inventory.VerificationScope, CheckedAt: &stamp}}
	for _, name := range []string{"configuration", "identity", "gateway", "response"} {
		item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: name, Status: "passed"})
	}
	inventory.Bindings = append(inventory.Bindings, item)
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed || report.Coverage != "complete" {
		t.Fatalf("configured outbound did not pass: %+v %v", report, err)
	}
	policy.AllowUnsupported = true
	inventory.Bindings[len(inventory.Bindings)-1].VerificationStatus = "failed"
	report, _ = Evaluate(inventory, policy, now)
	if report.Passed {
		t.Fatal("waiver hid failed outbound evidence")
	}
}
