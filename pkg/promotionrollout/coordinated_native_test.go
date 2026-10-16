package promotionrollout_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func TestCoordinatedNativeEvidenceIsScopedAndSourceBound(t *testing.T) {
	for _, scenario := range []string{"valid", "missing", "wrong_scope", "wrong_source", "failed", "wrong_inventory", "missing_scenario", "wrong_receipt", "admission_enabled"} {
		t.Run(scenario, func(t *testing.T) {
			selected := target()
			f := promotionrollouttest.New(t, selected)
			var report map[string]any
			if err := json.Unmarshal(f.Approval.CapabilityReports[promotionrollout.Coordinated], &report); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				delete(f.Approval.CapabilityReports, promotionrollout.Coordinated)
			case "wrong_scope":
				report["scope"] = promotionrollout.ServiceGraphScope
			case "wrong_source":
				report["source_sha"] = strings.Repeat("d", 40)
			case "failed":
				report["qualified"] = false
			case "wrong_inventory":
				report["tests"] = promotionrollout.NativeTests()
			case "missing_scenario":
				report["scenarios"] = map[string]any{}
			case "wrong_receipt":
				receipt := f.Approval.Gates["worker-replacement-resolve"]
				receipt.EvidenceSHA256 = strings.Repeat("d", 64)
				f.Approval.Gates["worker-replacement-resolve"] = receipt
			case "admission_enabled":
				report["admission_enabled"] = true
			}
			if scenario != "missing" {
				f.Approval.CapabilityReports[promotionrollout.Coordinated], _ = json.Marshal(report)
			}
			if err := os.WriteFile(f.ApprovalPath, unchecked(t, f.Approval, f.Private), 0600); err != nil {
				t.Fatal(err)
			}
			for _, capability := range []promotionrollout.Capability{promotionrollout.Workers, promotionrollout.Coordinated} {
				if f.Gate.Check(selected, capability) != (scenario == "valid") {
					t.Fatal("missing exact native scope was authorized", capability)
				}
			}
			if !f.Gate.Check(selected, promotionrollout.Health) || !f.Gate.Check(selected, promotionrollout.ServiceGraph) || !f.Gate.Check(selected, promotionrollout.Migrations) {
				t.Fatal("supplement widened unrelated admission requirements")
			}
		})
	}
}
