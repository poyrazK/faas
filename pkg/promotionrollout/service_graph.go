package promotionrollout

import (
	"encoding/json"
	"slices"
)

const ServiceGraphScope = "project-environment-promotion-only/promotion-service-graph"

func ServiceGraphTests() []string {
	return []string{"TestProjectEnvironmentPromotionServiceGraphFixtureMeta", "TestProjectEnvironmentPromotionServiceGraphMetal"}
}

// Supplemental reports cannot widen the base approval or qualify another
// capability. The signer reviews the native run and the three scoped receipts.
func serviceGraphEvidence(approval Approval) (nativeReport, bool) {
	var report nativeReport
	if Decode(approval.CapabilityReports[ServiceGraph], &report) != nil || report.SchemaVersion != Version ||
		report.SourceSHA != approval.SourceSHA || !validHex(report.LogSHA256, 32) || report.Scope != ServiceGraphScope ||
		!report.Qualified || report.AdmissionEnabled || len(report.Errors) != 0 || len(report.Tests) != 2 ||
		len(report.Scenarios) != 1 {
		return report, false
	}
	tests := ServiceGraphTests()
	if report.Tests[0] == report.Tests[1] || !slices.Contains(tests, report.Tests[0]) || !slices.Contains(tests, report.Tests[1]) {
		return report, false
	}
	var scenario map[string]json.RawMessage
	if json.Unmarshal(report.Scenarios[tests[1]], &scenario) != nil || string(scenario["schema_version"]) != "1" {
		return report, false
	}
	return report, true
}
