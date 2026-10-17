package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

const testCampaignCIVerdictFailed = 6

type testCampaignCICheck struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type testCampaignCIVerdict struct {
	Passed          bool                `json:"passed"`
	ExitCode        int                 `json:"exit_code"`
	Scope           string              `json:"scope"`
	ExpectedBatches int                 `json:"expected_batches"`
	VerifiedBatches int                 `json:"verified_batches"`
	Execution       testCampaignCICheck `json:"execution"`
	Policy          testCampaignCICheck `json:"policy"`
	Cleanup         testCampaignCICheck `json:"cleanup"`
}

func testCampaignPassedRuns(roots []testRunReceipt) (int, bool) {
	passed, failed := 0, false
	for _, root := range roots {
		failed = failed || root.Status != "passed" || testCampaignReceiptCleanupFailed(root, 0)
		var leaves []testRunReceipt
		collectTestFailureLeaves(root, "", &leaves)
		for _, leaf := range leaves {
			if leaf.Status == "passed" {
				passed++
			} else {
				failed = true
			}
		}
	}
	return passed, failed
}

func buildTestCampaignCIVerdict(report testCampaignStatusReport) testCampaignCIVerdict {
	v := testCampaignCIVerdict{Scope: report.Side, ExpectedBatches: len(report.Batches), ExitCode: testCampaignCIVerdictFailed,
		Execution: testCampaignCICheck{"incomplete", "Required batches lack complete verified passing receipts."},
		Policy:    testCampaignCICheck{"failed", "The evaluated policy has violations or unmet coverage/environment requirements."},
		Cleanup:   testCampaignCICheck{"incomplete", "Live resource verification is incomplete."}}
	failed := false
	for _, row := range report.Batches {
		failed = failed || row.ExecutionFailed || row.Cleanup == "recorded_failure"
		if (row.Status == "completed" || row.Status == "recovered") &&
			(row.Evidence == "verified_receipts" || row.Evidence == "verified_recovery_bundle") &&
			row.Estimated.Runs > 0 && int64(row.ExecutedRuns) == row.Estimated.Runs && row.PassedRuns == row.ExecutedRuns && !row.ExecutionFailed &&
			(row.Cleanup == "recorded_clean" || row.Cleanup == "live_absent") {
			v.VerifiedBatches++
		}
	}
	if failed {
		v.Execution = testCampaignCICheck{"failed", "Verified execution or recorded cleanup failures remain in the test evidence."}
	} else if v.ExpectedBatches > 0 && v.VerifiedBatches == v.ExpectedBatches {
		v.Execution = testCampaignCICheck{"passed", "Every batch in this scope has complete verified passing receipts."}
	}
	if report.PolicyPassed && report.PolicyViolations == 0 && len(report.PolicyBlocks) == 0 && len(report.CoverageGaps) == 0 && len(report.EnvironmentGaps) == 0 {
		v.Policy = testCampaignCICheck{"passed", "The policy passes on verified execution evidence."}
	}
	code, summary := testCampaignStatusCleanGate(report)
	if code == 0 {
		v.Cleanup = testCampaignCICheck{"passed", summary}
	} else if code == testCampaignStatusResourcesPresent {
		v.Cleanup = testCampaignCICheck{"failed", summary}
	} else {
		v.Cleanup.Reason = summary
	}
	blocked := false
	for _, issue := range report.Issues {
		blocked = blocked || issue.Code == "recovery_blocked"
	}
	if !report.SnapshotStable || !report.LineageComplete || blocked {
		v.VerifiedBatches = 0
		v.Execution = testCampaignCICheck{"incomplete", "Execution evidence changed or recovery lineage is unverified."}
		v.Policy = testCampaignCICheck{"incomplete", "Policy evidence changed or recovery lineage is unverified."}
	}
	v.Passed = v.Execution.State == "passed" && v.Policy.State == "passed" && v.Cleanup.State == "passed"
	if v.Passed {
		v.ExitCode = 0
	}
	return v
}

// Pin the files used for execution and policy before reading their contents.
// Watcher progress remains independently checked by each live snapshot.
type testCampaignCIProof map[string]string

func testCampaignCIFileHash(path string) string {
	file, err := openCustomerFile(path)
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		return "unreadable"
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, testBaselineReportLimit+1))
	if err != nil || n > testBaselineReportLimit {
		return "unreadable"
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (proof testCampaignCIProof) add(paths ...string) {
	for _, path := range paths {
		if path != "" {
			if _, ok := proof[path]; !ok {
				proof[path] = testCampaignCIFileHash(path)
			}
		}
	}
}

func (proof testCampaignCIProof) stable() bool {
	for path, hash := range proof {
		if hash == "unreadable" || testCampaignCIFileHash(path) != hash {
			return false
		}
	}
	return true
}
