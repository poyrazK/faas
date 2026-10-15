package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type testCampaignStatusIssue struct {
	Code string `json:"code"`
	Message string `json:"message"`
	Action string `json:"next_action"`
}

type testCampaignStatusBatch struct {
	ID string `json:"id"`
	Scenario string `json:"scenario"`
	Profile string `json:"profile"`
	Side string `json:"side"`
	Repetitions int `json:"repetitions"`
	Status string `json:"status"`
	CheckpointStatus string `json:"checkpoint_status,omitempty"`
	Evidence string `json:"evidence"`
	Cleanup string `json:"cleanup"`
	ExecutedRuns int `json:"verified_executed_runs"`
	Estimated testCampaignPlanBudget `json:"estimated"`
	ReportSHA256 string `json:"report_sha256,omitempty"`
	Action string `json:"next_action"`
}

type testCampaignStatusCoverage struct {
	Name string `json:"name"`
	Configuration string `json:"configuration_sha256,omitempty"`
	Context testFailureGroupKey `json:"context"`
	BaselineRemaining int `json:"baseline_remaining_runs"`
	CurrentRemaining int `json:"current_remaining_runs"`
	Action string `json:"next_action"`
}

type testCampaignStatusEnvironment struct {
	Name string `json:"name"`
	Configuration string `json:"configuration_sha256,omitempty"`
	Context testFailureGroupKey `json:"context"`
	Reason string `json:"reason"`
	BaselineExecuted int `json:"baseline_executed_runs"`
	BaselineSatisfied int `json:"baseline_satisfied_runs"`
	CurrentExecuted int `json:"current_executed_runs"`
	CurrentSatisfied int `json:"current_satisfied_runs"`
	Action string `json:"next_action"`
}

func newTestCampaignStatusCounts() map[string]int {
	return map[string]int{"completed":0,"running":0,"pending":0,"blocked":0,"recovered":0,"awaiting_reconciliation":0}
}

type testCampaignStatusLineage struct {
	Allocation testCampaignPlanBudget `json:"parent_side_allocation"`
	Reserved testCampaignPlanBudget `json:"parent_reserved_estimate"`
	Remaining testCampaignPlanBudget `json:"parent_remaining_allocation"`
	Directory string `json:"directory"`
	PlanSHA256 string `json:"plan_sha256"`
	CheckpointSHA256 string `json:"checkpoint_sha256"`
	Side string `json:"side"`
	Revision string `json:"revision"`
	Verified bool `json:"verified"`
}

type testCampaignStatusReport struct {
	SchemaVersion int `json:"schema_version"`
	Informational bool `json:"informational"`
	ReadOnly bool `json:"read_only"`
	ObservedAt time.Time `json:"observed_at"`
	PlanSHA256 string `json:"plan_sha256"`
	Directory string `json:"directory,omitempty"`
	Side string `json:"side"`
	Revision string `json:"revision,omitempty"`
	LockPresent bool `json:"lock_present"`
	SnapshotStable bool `json:"snapshot_stable"`
	Sealed bool `json:"sealed"`
	Halted bool `json:"halted"`
	Counts map[string]int `json:"batch_counts"`
	Allocation testCampaignPlanBudget `json:"allocated_estimate"`
	Reserved testCampaignPlanBudget `json:"reserved_estimate"`
	Remaining testCampaignPlanBudget `json:"remaining_allocation"`
	PlanBudget testCampaignPlanBudget `json:"plan_budget"`
	PolicyPassed bool `json:"policy_passed"`
	PolicyViolations int `json:"policy_violations"`
	PolicyBlocks []testCampaignStatusIssue `json:"policy_blockers"`
	RecoveryBundle string `json:"recovery_bundle,omitempty"`
	ContinuationSHA256 string `json:"continuation_plan_sha256,omitempty"`
	EnvironmentGaps []testCampaignStatusEnvironment `json:"environment_gaps"`
	CoverageGaps []testCampaignStatusCoverage `json:"coverage_gaps"`
	Batches []testCampaignStatusBatch `json:"batches"`
	Lineage []testCampaignStatusLineage `json:"recovery_lineage"`
	LineageComplete bool `json:"lineage_complete"`
	Issues []testCampaignStatusIssue `json:"issues"`
	Notes []string `json:"notes"`
}

func testCampaignCheckpointLineageMatches(checkpoint testCampaignCheckpoint,plan testCampaignPolicyPlan) bool {
	if checkpoint.LineageVersion == 0 { return true }
	return checkpoint.LineageVersion == 1 && testCampaignJSONHash(checkpoint.Recovery) == testCampaignJSONHash(plan.Recovery)
}

func testCampaignStatusTrace(plan testCampaignPolicyPlan) ([]testCampaignStatusLineage,bool,[]testCampaignStatusIssue) {
	rows := []testCampaignStatusLineage{}
	issues := []testCampaignStatusIssue{}
	link,childHash := plan.Recovery,testCampaignJSONHash(plan)
	seen := make(map[string]bool)
	for link != nil {
		row := testCampaignStatusLineage{Allocation:link.Allocation,Reserved:link.Reserved,Remaining:link.Remaining,Directory:link.ParentDirectory,PlanSHA256:link.ParentPlanSHA256,
			CheckpointSHA256:link.CheckpointSHA256,Side:link.Side,Revision:link.Revision}
		key := filepath.Clean(link.ParentDirectory)
		if len(rows) >= 32 || seen[key] {
			issues = append(issues,testCampaignStatusIssue{Code:"lineage_limit",Message:"Recovery chain is cyclic or exceeds 32 parents.",Action:"Inspect the saved parent plans and restore a consistent recovery chain."})
			return rows,false,issues
		}
		seen[key] = true
		var checkpoint testCampaignCheckpoint
		var seal testCampaignRecoverySeal
		valid := readTestCampaignJSON(filepath.Join(key,"checkpoint.json"),&checkpoint) == nil &&
			readTestCampaignJSON(filepath.Join(key,testCampaignRecoverySealName),&seal) == nil &&
			seal.SchemaVersion == 1 && seal.ContinuationSHA256 == childHash &&
			seal.ParentPlanSHA256 == link.ParentPlanSHA256 && seal.CheckpointSHA256 == link.CheckpointSHA256 &&
			seal.ReconciliationSHA256 == link.ReconciliationSHA256 &&
			testCampaignJSONHash(checkpoint) == link.CheckpointSHA256 && checkpoint.PlanSHA256 == link.ParentPlanSHA256 &&
			checkpoint.Side == link.Side && checkpoint.Revision == link.Revision && seal.Blocked == link.Blocked
		row.Verified = valid
		rows = append(rows,row)
		if !valid {
			issues = append(issues,testCampaignStatusIssue{Code:"lineage_unverified",Message:"A parent checkpoint or reconciliation seal no longer matches.",Action:"Restore the matching parent artifacts before executing the continuation."})
			return rows,false,issues
		}
		if checkpoint.LineageVersion != 1 {
			issues = append(issues,testCampaignStatusIssue{Code:"legacy_lineage",Message:"An older parent checkpoint does not record earlier recovery links.",Action:"Retain and inspect its original saved plan to trace earlier history."})
			return rows,false,issues
		}
		childHash,link = checkpoint.PlanSHA256,checkpoint.Recovery
	}
	return rows,true,issues
}

func newTestCampaignStatusLedger() testReportMergeState {
	return testReportMergeState{SourceIdentities:make(map[testReportSourceContext]string),RunIDs:make(map[string]string)}
}

// Validate a candidate against a copy so rejected evidence cannot poison later
// batches' identity accounting. Coverage always uses verified receipt objects.
func appendTestCampaignStatusEvidence(existing,candidate []testRunReceipt) ([]testRunReceipt,error) {
	state := newTestCampaignStatusLedger()
	for index,r := range append(append([]testRunReceipt{},existing...),candidate...) {
		if err := state.checkReceipt(r,fmt.Sprint(index),0); err != nil { return existing,errors.New("duplicate, mixed-source or incomplete execution evidence") }
	}
	return append(existing,candidate...),nil
}

func buildTestCampaignStatus(plan testCampaignPolicyPlan,checkpoint *testCampaignCheckpoint,directory,side string,
	policy testCampaignPolicy,scenarios map[string]testScenario,before,after []testRunReceipt) (testCampaignStatusReport,error) {
	report := testCampaignStatusReport{SchemaVersion:1,Informational:true,ReadOnly:true,ObservedAt:time.Now().UTC(),
		PlanSHA256:testCampaignJSONHash(plan),Directory:directory,Side:side,SnapshotStable:true,Counts:newTestCampaignStatusCounts(),
		EnvironmentGaps:[]testCampaignStatusEnvironment{},CoverageGaps:[]testCampaignStatusCoverage{},Batches:[]testCampaignStatusBatch{},
		Issues:[]testCampaignStatusIssue{},Notes:[]string{
			"Checkpoint running state and lock presence do not prove process liveness.",
			"Reserved and remaining amounts are conservative planning estimates, not measured billing.",
			"Coverage is recomputed from verified receipts; planned repetitions and saved policy summaries do not count as evidence.",
			"The policy verdict covers the verified evidence subset; it does not imply every planned batch finished.",
			"The sealed flag records marker presence. A matching recovery bundle verifies adoption and the published continuation.",
			"This is a read-only snapshot. Do not use it as an execution or cleanup gate.",
		}}
	if plan.Costs != nil { report.PlanBudget = plan.Costs.Budget }
	if checkpoint != nil {
		report.Side,report.Revision,report.Halted = checkpoint.Side,checkpoint.Revision,checkpoint.Halted
		if checkpoint.PlanSHA256 != report.PlanSHA256 || checkpoint.SchemaVersion != 1 ||
			(checkpoint.Side != "baseline" && checkpoint.Side != "current") || checkpoint.Batches == nil ||
			!testCampaignCheckpointLineageMatches(*checkpoint,plan) ||
			(plan.Recovery != nil && (plan.Recovery.Side != checkpoint.Side || plan.Recovery.Revision != checkpoint.Revision)) {
			return report,errors.New("checkpoint does not match the plan, side or lineage")
		}
	}
	if report.Side == "" { report.Side = "all" }
	if directory != "" {
		_,err := os.Lstat(filepath.Join(directory,"runner.lock"))
		if err != nil && !os.IsNotExist(err) { return report,err }
		report.LockPresent = err == nil
		_,err = os.Lstat(filepath.Join(directory,testCampaignRecoverySealName))
		if err != nil && !os.IsNotExist(err) { return report,err }
		report.Sealed = err == nil
	}
	report.Lineage,report.LineageComplete,report.Issues = testCampaignStatusTrace(plan)
	if plan.Recovery != nil && plan.Recovery.Blocked {
		report.Issues = append(report.Issues,testCampaignStatusIssue{Code:"recovery_blocked",Message:"This continuation was published with unresolved recovery evidence.",Action:"Resolve the parent reconciliation diagnostics and refresh its blocked bundle."})
	}
	if report.Sealed { report.Issues = append(report.Issues,testCampaignStatusIssue{Code:"campaign_sealed",Message:"The original scheduler is frozen by a reconciliation seal.",Action:"Use its continuation or supply --recovery-bundle to inspect adopted evidence."}) }
	if report.Halted { report.Issues = append(report.Issues,testCampaignStatusIssue{Code:"campaign_halted",Message:"The checkpoint halted scheduling.",Action:"Reconcile execution and cleanup evidence before creating a continuation."}) }
	before,err := appendTestCampaignStatusEvidence(nil,before)
	if err != nil { return report,err }
	after,err = appendTestCampaignStatusEvidence(nil,after)
	if err != nil { return report,err }
	allowed := make(map[string]bool)
	for _,batch := range plan.Batches {
		if checkpoint != nil && batch.Side != checkpoint.Side { continue }
		allowed[batch.ID] = true
	}
	if checkpoint != nil {
		for id,saved := range checkpoint.Batches {
			if !allowed[id] || (saved.Status != "done" && saved.Status != "running" && saved.Status != "uncertain") {
				return report,errors.New("checkpoint contains unknown batches or statuses")
			}
		}
	}
	readBudget := int64(2*testBaselineReportLimit)
	limitReported := false
	for _,batch := range plan.Batches {
		if report.Side != "all" && batch.Side != report.Side { continue }
		row := testCampaignStatusBatch{ID:batch.ID,Scenario:batch.Scenario,Profile:batch.Profile,Side:batch.Side,
			Repetitions:batch.Repetitions,Estimated:batch.Estimated,Status:"pending",Evidence:"none",Cleanup:"not_started",
			Action:"Run the saved plan for this side at its intended revision."}
		if err := testCampaignAddRecoveryCost(&report.Allocation,batch.Estimated); err != nil { return report,err }
		var saved testCampaignCheckpointBatch
		recorded := false
		if checkpoint != nil { saved,recorded = checkpoint.Batches[batch.ID]; row.CheckpointStatus = saved.Status }
		artifact := false
		if directory != "" {
			for _,suffix := range []string{".json",".log"} {
				_,err := os.Lstat(filepath.Join(directory,"batch-"+batch.ID+suffix))
				if err != nil && !os.IsNotExist(err) { return report,err }
				artifact = artifact || err == nil
			}
		}
		if recorded || artifact {
			if err := testCampaignAddRecoveryCost(&report.Reserved,batch.Estimated); err != nil { return report,err }
			row.Status,row.Evidence,row.Cleanup = "blocked","unverified","unknown"
			row.Action = "Reconcile this batch; do not repeat uncertain execution."
			if saved.Status == "running" && report.LockPresent { row.Status = "running"; row.Evidence = "checkpoint_only" }
			if checkpoint != nil && directory != "" {
				path := filepath.Join(directory,"batch-"+batch.ID+".json")
				if info,err := os.Stat(path); err == nil {
					if info.Size() > readBudget {
						row.Status,row.Evidence,row.Action = "blocked","inspection_limit","Inspect a smaller set of receipt files; the status report read limit was reached."
						if !limitReported { report.Issues = append(report.Issues,testCampaignStatusIssue{Code:"inspection_limit",Message:"Batch reports exceed the 128 MiB status read allowance.",Action:"Inspect smaller campaigns or use a bound recovery bundle."}); limitReported = true }
						report.Batches = append(report.Batches,row)
						continue
					}
					readBudget -= info.Size()
				}
				hash,cleanup,verifyErr := validateTestCampaignBatchReport(path,batch,scenarios[batch.Scenario],checkpoint.Revision)
				if verifyErr == nil {
					row.ReportSHA256 = hash
					if saved.Status != "done" || saved.ReportSHA256 == hash {
						row.Cleanup = "recorded_clean"
						if cleanup { row.Cleanup = "recorded_failure" }
					}
					if saved.Status == "done" && saved.ReportSHA256 == hash {
						receipts,readHash,readErr := readTestBaselineReport(path,nil)
						if readErr == nil && readHash == hash {
							var addErr error
							if batch.Side == "baseline" { before,addErr = appendTestCampaignStatusEvidence(before,receipts) } else { after,addErr = appendTestCampaignStatusEvidence(after,receipts) }
							if addErr == nil {
								row.Status,row.Evidence,row.ExecutedRuns,row.Action = "completed","verified_receipts",int(batch.Estimated.Runs),"No further execution is needed for this batch."
								if cleanup { row.Status,row.Action = "blocked","Inspect and resolve the recorded cleanup failure before continuing." }
							} else { report.Issues = append(report.Issues,testCampaignStatusIssue{Code:"evidence_conflict",Message:"Batch "+batch.ID+" overlaps or conflicts with other receipts.",Action:"Restore distinct reports from the intended source revision."}) }
						}
					} else if saved.Status != "done" && !cleanup {
						row.Status,row.Evidence = "awaiting_reconciliation","complete_report_uncheckpointed"
					}
				}
			}
		}
		if row.Status == "pending" && (report.Sealed || report.Halted || (plan.Recovery != nil && plan.Recovery.Blocked)) {
			row.Status = "blocked"
			if report.Sealed { row.Action = "Use the sealed continuation; this original scheduler is frozen." } else { row.Action = "Resolve the campaign's recovery blockers before scheduling work." }
		}
		report.Batches = append(report.Batches,row)
	}
	report.Remaining,err = testCampaignRecoveryRemaining(report.Allocation,report.Reserved)
	if err != nil { return report,err }
	for _,row := range report.Batches { report.Counts[row.Status]++ }
	result,err := evaluateTestCampaignPolicy(policy,before,after,"","")
	if err != nil { return report,err }
	applyTestCampaignStatusPolicy(&report,result,plan)
	return report,nil
}

func applyTestCampaignStatusPolicy(report *testCampaignStatusReport,result testCampaignPolicyResult,plan testCampaignPolicyPlan) {
	report.PolicyPassed,report.PolicyViolations = result.Passed,len(result.Violations)
	report.PolicyBlocks = []testCampaignStatusIssue{}
	for _,violation := range result.Violations {
		action := "Inspect the matching failure evidence, resolve the regression and reevaluate with fresh receipts."
		switch violation.Rule {
		case "explicit_required_coverage":
			action = "Resolve the coverage gaps shown below on both revision sides."
		case "required_environment","explicit_required_environment","environment_mismatch","environment_unknown":
			action = "Resolve differing or unavailable environment evidence before comparing behavior."
		case "insufficient_samples":
			action = "Collect enough qualifying executions for the selected policy's sample minimum."
		case "not_comparable":
			action = "Align configuration and environment evidence or collect a matching baseline."
		}
		report.PolicyBlocks = append(report.PolicyBlocks,testCampaignStatusIssue{Code:violation.Rule,Message:violation.Reason,Action:action})
	}
	report.CoverageGaps = []testCampaignStatusCoverage{}
	report.EnvironmentGaps = []testCampaignStatusEnvironment{}
	for _,coverage := range result.RequiredCoverage {
		if coverage.Passed { continue }
		gap := testCampaignStatusCoverage{Name:coverage.Name,Configuration:coverage.Configuration,Context:coverage.Context,
			BaselineRemaining:max(0,coverage.MinExecutedRuns-coverage.Baseline.CoveredRuns),
			CurrentRemaining:max(0,coverage.MinExecutedRuns-coverage.Current.CoveredRuns),
			Action:"Collect qualifying native execution evidence on each deficient revision side, then reevaluate policy."}
		for _,entry := range plan.Entries {
			if entry.Requirement != gap.Name || entry.Configuration != gap.Configuration || entry.Context.Profile != gap.Context.Profile { continue }
			switch entry.Status {
			case "unavailable_configuration","unsatisfiable":
				gap.Action = "Restore the manifest/configuration that declares this required context before planning more runs."
			case "environment_blocked","partially_environment_blocked":
				gap.Action = "Resolve the environment prerequisites before collecting additional coverage."
			case "needs_review","unsupported":
				gap.Action = "Review the unsupported assertion or engine before scheduling coverage."
			}
		}
		if report.Remaining.Runs == 0 && (report.Reserved.Runs > 0 || (plan.Recovery != nil && plan.Recovery.Remaining.Runs == 0)) {
			gap.Action += " The current allocation is exhausted; additional work needs an explicitly budgeted new plan."
		}
		report.CoverageGaps = append(report.CoverageGaps,gap)
	}
	for _,environment := range result.RequiredEnvironments {
		if environment.Passed { continue }
		report.EnvironmentGaps = append(report.EnvironmentGaps,testCampaignStatusEnvironment{
			Name:environment.Name,Configuration:environment.Configuration,Context:environment.Context,Reason:environment.Reason,
			BaselineExecuted:environment.Baseline.ExecutedRuns,BaselineSatisfied:environment.Baseline.SatisfiedRuns,
			CurrentExecuted:environment.Current.ExecutedRuns,CurrentSatisfied:environment.Current.SatisfiedRuns,
			Action:"Collect matching native environment evidence on each deficient side and resolve missing or differing settings.",
		})
	}
}
