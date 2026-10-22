package bindingcheck

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObjectStorageOperationRequirements(t *testing.T) {
	for _, required := range []string{"read", "write", "read_write"} {
		for _, evidence := range []string{"", "read", "write", "read_write"} {
			t.Run(required+"/"+evidence, func(t *testing.T) {
				inventory, policy, now := fixture()
				item := &inventory.Bindings[2]
				item.Access = "read_write"
				item.Verification.ObjectStorageMode = evidence
				if evidence == "write" || evidence == "read_write" {
					item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "upload", Status: "passed"}, api.BindingVerificationCheck{Name: "cleanup", Status: "passed"})
				}
				if evidence == "read_write" {
					item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "download", Status: "passed"})
				}
				policy.ObjectStorageRequirements = map[string]string{"ASSETS": required}
				report, err := Evaluate(inventory, policy, now)
				mode := evidence
				if mode == "" {
					mode = "read"
				}
				want := storageModeCovers(mode, required)
				if err != nil || report.Passed != want {
					t.Fatalf("passed=%t want=%t: %+v %v", report.Passed, want, report, err)
				}
				if !want && !slices.Contains(codes(report.Blockers), "object_storage_operations_missing") {
					t.Fatalf("operation blocker missing: %+v", report)
				}
			})
		}
	}
}

func TestObjectStorageRequirementsCannotWaiveMissingOrInvalidEvidence(t *testing.T) {
	for _, scenario := range []string{"missing binding", "wrong scope", "duplicate binding", "wrong permission", "missing cleanup", "duplicate stage", "failed upload", "unknown mode", "legacy with write stages", "stale revision", "expired", "old deployment", "rotation", "missing application ack"} {
		t.Run(scenario, func(t *testing.T) {
			inventory, policy, now := fixture()
			item := &inventory.Bindings[2]
			item.Access = "read_write"
			item.Verification.ObjectStorageMode = "read_write"
			item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "upload", Status: "passed"}, api.BindingVerificationCheck{Name: "cleanup", Status: "passed"}, api.BindingVerificationCheck{Name: "download", Status: "passed"})
			policy.AllowUnsupported = true
			policy.ObjectStorageRequirements = map[string]string{"ASSETS": "read_write"}
			switch scenario {
			case "missing binding":
				inventory.Bindings = inventory.Bindings[:2]
			case "wrong scope":
				item.Scope = "staging"
			case "duplicate binding":
				inventory.Bindings = append(inventory.Bindings, *item)
			case "wrong permission":
				item.Access = "write"
			case "missing cleanup":
				item.Verification.Checks = append(item.Verification.Checks[:6], item.Verification.Checks[7:]...)
			case "duplicate stage":
				item.Verification.Checks[6] = item.Verification.Checks[5]
			case "failed upload":
				item.Verification.Checks[5].Status = "failed"
			case "unknown mode":
				item.Verification.ObjectStorageMode = "future"
			case "legacy with write stages":
				item.Verification.ObjectStorageMode = ""
			case "stale revision":
				item.VerificationStatus = "stale"
			case "expired":
				at := now.Add(-policy.MaxVerificationAge - 1)
				item.Verification.CheckedAt = &at
			case "old deployment":
				item.Verification.DeploymentID = "old"
			case "rotation":
				pending := true
				item.RotationPending = &pending
			case "missing application ack":
				policy.RequireApplicationAck = true
			}
			report, err := Evaluate(inventory, policy, now)
			if err != nil || report.Passed || len(report.Blockers) == 0 {
				t.Fatalf("invalid evidence passed: %+v %v", report, err)
			}
		})
	}
}

func TestObjectStorageWriteEvidenceWithoutNewPolicy(t *testing.T) {
	inventory, policy, now := fixture()
	item := &inventory.Bindings[2]
	item.Verification.ObjectStorageMode = "write"
	item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "upload", Status: "passed"}, api.BindingVerificationCheck{Name: "cleanup", Status: "passed"})
	report, err := Evaluate(inventory, policy, now)
	if err != nil || !report.Passed {
		t.Fatalf("valid write canary rejected by generic gate: %+v %v", report, err)
	}
	policy.ObjectStorageRequirements = map[string]string{"ASSETS": "delete"}
	if _, err := Evaluate(inventory, policy, now); err == nil {
		t.Fatal("unknown policy accepted")
	}
}
