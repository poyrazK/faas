package promotionrollout

import (
	"encoding/json"
	"slices"
)

const CoordinatedScope = "project-environment-promotion-only/promotion-coordinated"

func CoordinatedTests() []string {
	return []string{"TestProjectEnvironmentPromotionCoordinatedFixtureMeta", "TestProjectEnvironmentPromotionCoordinatedSuccessMetal", "TestProjectEnvironmentPromotionCoordinatedRollbackMetal", "TestProjectEnvironmentPromotionCoordinatedResolveMetal"}
}

func coordinatedEvidence(approval Approval) (nativeReport, bool) {
	var report nativeReport
	tests := CoordinatedTests()
	if Decode(approval.CapabilityReports[Coordinated], &report) != nil || report.SchemaVersion != Version || report.SourceSHA != approval.SourceSHA || !validHex(report.LogSHA256, 32) || report.Scope != CoordinatedScope || !report.Qualified || report.AdmissionEnabled || len(report.Errors) != 0 || len(report.Tests) != len(tests) || len(report.Scenarios) != len(tests)-1 {
		return report, false
	}
	seen := map[string]bool{}
	for _, name := range report.Tests {
		if seen[name] || !slices.Contains(tests, name) {
			return report, false
		}
		seen[name] = true
		if name == tests[0] {
			continue
		}
		var scenario map[string]json.RawMessage
		if json.Unmarshal(report.Scenarios[name], &scenario) != nil || string(scenario["schema_version"]) != "1" {
			return report, false
		}
	}
	return report, true
}
