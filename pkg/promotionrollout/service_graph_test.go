package promotionrollout_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func TestServiceGraphRequiresItsOwnSourceBoundNativeEvidence(t *testing.T) {
	for _, scenario := range []string{"valid", "missing", "wrong_scope", "wrong_source", "failed", "wrong_inventory", "wrong_receipt"} {
		t.Run(scenario, func(t *testing.T) {
			selected := target()
			f := promotionrollouttest.New(t, selected)
			var report map[string]any
			if err := json.Unmarshal(f.Approval.CapabilityReports[promotionrollout.ServiceGraph], &report); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				delete(f.Approval.CapabilityReports, promotionrollout.ServiceGraph)
			case "wrong_scope":
				report["scope"] = "project-environment-promotion-only"
			case "wrong_source":
				report["source_sha"] = strings.Repeat("d", 40)
			case "failed":
				report["qualified"] = false
			case "wrong_inventory":
				report["tests"] = []string{promotionrollout.ServiceGraphTests()[0], promotionrollout.ServiceGraphTests()[0]}
			case "wrong_receipt":
				receipt := f.Approval.Gates["service-graph-routing"]
				receipt.EvidenceSHA256 = strings.Repeat("d", 64)
				f.Approval.Gates["service-graph-routing"] = receipt
			}
			if scenario != "missing" {
				f.Approval.CapabilityReports[promotionrollout.ServiceGraph], _ = json.Marshal(report)
			}
			// Deliberately sign unvalidated unit fixtures to exercise admission.
			if err := os.WriteFile(f.ApprovalPath, unchecked(t, f.Approval, f.Private), 0600); err != nil {
				t.Fatal(err)
			}
			gate := f.Gate
			if !gate.Check(selected, promotionrollout.Health) {
				t.Fatal("supplemental evidence changed basic health admission")
			}
			if gate.Check(selected, promotionrollout.ServiceGraph) != (scenario == "valid") {
				t.Fatal("service graph evidence was not enforced")
			}
		})
	}
}
