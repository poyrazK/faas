package promotionrollout

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// NativeTests pins the complete report format produced by the promotion CI
// validator. A per-partition or earlier inventory cannot authorize rollout.
func NativeTests() []string {
	return []string{"TestProjectEnvironmentPromotionFixtureMeta", "TestProjectEnvironmentPromotionSuccessMetal", "TestProjectEnvironmentPromotionRollbackMetal",
		"TestProjectEnvironmentPromotionControlsMetal", "TestProjectEnvironmentPromotionConcurrentMetal", "TestProjectEnvironmentPromotionAdmissionMetal",
		"TestProjectEnvironmentPromotionMigrationSuccessMetal", "TestProjectEnvironmentPromotionMigrationRollbackMetal", "TestProjectEnvironmentPromotionMigrationFailureMetal", "TestProjectEnvironmentPromotionMigrationRecoveryMetal",
		"TestProjectEnvironmentPromotionPublicationMetal"}
}

type nativeReport struct {
	SchemaVersion    int                        `json:"schema_version"`
	SourceSHA        string                     `json:"source_sha"`
	LogSHA256        string                     `json:"log_sha256"`
	Scope            string                     `json:"scope"`
	Qualified        bool                       `json:"qualified"`
	AdmissionEnabled bool                       `json:"admission_enabled"`
	Tests            []string                   `json:"tests"`
	Scenarios        map[string]json.RawMessage `json:"scenarios"`
	Errors           []string                   `json:"errors"`
}

func validateNative(raw []byte, revision string) bool {
	var report nativeReport
	if Decode(raw, &report) != nil || report.SchemaVersion != Version || report.SourceSHA != revision ||
		!validHex(report.LogSHA256, 32) || report.Scope != "project-environment-promotion-only" || !report.Qualified || report.AdmissionEnabled ||
		len(report.Errors) != 0 || len(report.Tests) != len(NativeTests()) || len(report.Scenarios) != len(NativeTests())-1 {
		return false
	}
	seen := map[string]bool{}
	for _, name := range report.Tests {
		if seen[name] || !slices.Contains(NativeTests(), name) {
			return false
		}
		seen[name] = true
		if name == "TestProjectEnvironmentPromotionFixtureMeta" {
			continue
		}
		var scenario map[string]json.RawMessage
		if json.Unmarshal(report.Scenarios[name], &scenario) != nil || len(scenario) == 0 || string(scenario["schema_version"]) != "1" {
			return false
		}
	}
	return true
}

func validTargets(targets []Target) bool {
	if len(targets) == 0 || len(targets) > api.PromotionRolloutMaxTargets {
		return false
	}
	seen := map[Target]bool{}
	for _, target := range targets {
		if !validID(target.AccountID) || !validID(target.ProjectID) || seen[target] {
			return false
		}
		seen[target] = true
	}
	return true
}

func validCapabilities(capabilities []Capability) bool {
	if len(capabilities) == 0 {
		return false
	}
	seen := map[Capability]bool{}
	for _, capability := range capabilities {
		if !slices.Contains(Capabilities(), capability) || seen[capability] {
			return false
		}
		seen[capability] = true
	}
	return (!seen[WorkerSet] || seen[Workers]) && seen[Health] && (!seen[Coordinated] || seen[Workers] && seen[Migrations]) // composition requires both scopes
}

func validateApproval(approval Approval, revision string, now time.Time) string {
	if approval.SchemaVersion != Version || !validHex(approval.SourceSHA, 20) || !validTargets(approval.Targets) || !validCapabilities(approval.Capabilities) {
		return "approval_invalid"
	}
	if approval.SourceSHA != revision || !validHex(revision, 20) {
		return "revision_mismatch"
	}
	if approval.IssuedAt.IsZero() || approval.IssuedAt.After(now) || !approval.ExpiresAt.After(approval.IssuedAt) || approval.ExpiresAt.Sub(approval.IssuedAt) > api.PromotionRolloutApprovalMaxTTL {
		return "approval_time_invalid"
	}
	if !now.Before(approval.ExpiresAt) {
		return "approval_expired"
	}
	if !validateNative(approval.NativeReport, revision) {
		return "native_evidence_incomplete"
	}
	return ""
}

func missingGates(approval Approval, capability Capability) []string {
	var reasons []string
	var graph nativeReport
	if capability == ServiceGraph {
		var qualified bool
		graph, qualified = serviceGraphEvidence(approval)
		if !qualified {
			reasons = append(reasons, "missing_evidence:service-graph-native")
		}
	}
	var coordinated nativeReport
	if capability == Coordinated || capability == Workers || capability == WorkerSet {
		var qualified bool
		coordinated, qualified = coordinatedEvidence(approval)
		if !qualified {
			reasons = append(reasons, "missing_evidence:coordinated-native")
		}
	}
	for _, name := range RequiredGates(capability) {
		receipt, exists := approval.Gates[name]
		if !exists || receipt.Outcome != "passed" || !validHex(receipt.EvidenceSHA256, 32) ||
			capability == ServiceGraph && (name == "service-graph-routing" || name == "service-graph-recovery" || name == "service-graph-isolation") && receipt.EvidenceSHA256 != graph.LogSHA256 ||
			(name == "coordinated-migration-workers" || name == "worker-replacement-resolve") && receipt.EvidenceSHA256 != coordinated.LogSHA256 {
			reasons = append(reasons, "missing_evidence:"+name)
		}
	}
	return reasons
}
