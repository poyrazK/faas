package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func plannerPreflightTestInputs() (testCampaignPolicy, map[string]testScenario, *testPlannerPreflight) {
	policy, scenarios, _ := environmentPlanTestInputs()
	memory := 128
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{RAMMB: &memory}
	_, _, targets, client := environmentPreflightTestInputs()
	digest := strings.Repeat("b", 64)
	report := inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, digest)
	return policy, scenarios, &testPlannerPreflight{Report: report, ReportSHA256: strings.Repeat("a", 64),
		Targets: targets, TargetsSHA256: digest, Side: "current", Now: report.FinishedAt.Add(time.Second), MaxAge: 15*time.Minute}
}

func TestPlannerPreflightResolvesOnlySelectedSideWithoutCoverageCredit(t *testing.T) {
	// adr: 984 — fresh live observations resolve planning prerequisites, never executed coverage.
	for _, side := range []string{"current", "baseline"} {
		policy, scenarios, source := plannerPreflightTestInputs()
		source.Side = side
		plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil, source)
		if err != nil || plan.Preflight == nil || plan.Preflight.Side != side || len(plan.Entries) != 1 {
			t.Fatalf("unexpected preflight-aware plan: %+v %v", plan, err)
		}
		entry := plan.Entries[0]
		if entry.CurrentCovered != 0 || entry.BaselineCovered != 0 || entry.CurrentAdditional != 2 || entry.BaselineAdditional != 2 ||
			len(entry.Suggestions) != 1 || entry.Suggestions[0].Side != side || entry.Status != "partially_environment_blocked" {
			t.Fatalf("preflight gained coverage or crossed sides: %+v", entry)
		}
		resolution := plan.Preflight.Resolutions[0]
		if resolution.Status != "resolved" || len(resolution.ResolvedFields) != 1 ||
			resolution.ResolvedFields[0] != "workloads.checkout.ram_mb" || plan.Preflight.ExecutionEvidence {
			t.Fatal("resolved fields or evidence classification changed")
		}
	}
}

func TestPlannerPreflightRejectsStaleMismappedOrInvalidDocuments(t *testing.T) {
	// adr: 984 — preflight scope and observation time must remain bound to explicit inputs.
	for _, kind := range []string{"stale", "mapping", "future", "old-workload", "execution", "duplicate", "side", "age"} {
		t.Run(kind, func(t *testing.T) {
			policy, scenarios, source := plannerPreflightTestInputs()
			switch kind {
			case "stale":
				source.Now = source.Now.Add(16*time.Minute)
			case "mapping":
				source.TargetsSHA256 = strings.Repeat("c", 64)
			case "future":
				source.Report.FinishedAt = source.Now.Add(time.Second)
			case "old-workload":
				source.Report.Requirements[0].Workloads[0].ObservedAt = source.Report.StartedAt.Add(-time.Second)
			case "execution":
				source.Report.ExecutionEvidence = true
			case "duplicate":
				source.Report.Requirements = append(source.Report.Requirements, source.Report.Requirements[0])
			case "side":
				source.Side = "both"
			case "age":
				source.MaxAge = 0
			}
			if _, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil, source); err == nil {
				t.Fatalf("accepted invalid %s preflight", kind)
			}
		})
	}
}

func TestPlannerPreflightRechecksRawValuesAgainstCurrentRequirements(t *testing.T) {
	// adr: 984 — saved ready flags or old expected values cannot authorize changed requirements.
	policy, scenarios, source := plannerPreflightTestInputs()
	memory := 256
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{RAMMB: &memory}
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil, source)
	if err != nil || plan.Preflight.Resolutions[0].Status != "rejected" || len(plan.Entries[0].Suggestions) != 0 {
		t.Fatal("saved ready flag overrode a changed memory requirement")
	}
	policy, scenarios, source = plannerPreflightTestInputs()
	source.Report.Ready, source.Report.Requirements[0].Ready = false, false
	for i := range source.Report.Requirements[0].Checks { source.Report.Requirements[0].Checks[i].Status = "unavailable" }
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil, source)
	if err != nil || plan.Preflight.Resolutions[0].Status != "resolved" {
		t.Fatal("derived status flags replaced raw field verification")
	}
	policy, scenarios, source = plannerPreflightTestInputs()
	profile := "cold"
	source.Report.Requirements[0].IntendedContext.Profile = &profile
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil, source)
	if err != nil || plan.Preflight.Resolutions[0].Status != "rejected" {
		t.Fatal("different intended selectors resolved this context")
	}
}

func TestPlannerPreflightCannotWaiveReportReplacementOrVersionLimits(t *testing.T) {
	// adr: 984 — live checks cannot remove noncompliant runs or manufacture future version evidence.
	policy, scenarios, source := plannerPreflightTestInputs()
	_, _, good := environmentPlanTestInputs()
	bad := environmentTestReceipt("bad", true)
	bad.Scenario, bad.Profile, bad.ConfigurationSHA256 = good.Scenario, good.Profile, good.ConfigurationSHA256
	workload := bad.Environment.Workloads["checkout"]
	workload.RAMMB = 256
	bad.Environment.Workloads["checkout"] = workload
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", []testRunReceipt{good}, []testRunReceipt{bad}, source)
	if err != nil || plan.Environments[0].Current.Action != "platform_change_required" ||
		!plan.Environments[0].Current.ReportReplacementRequired || plan.Preflight.Resolutions[0].Status != "not_applicable" {
		t.Fatal("live metadata waived a bad execution report")
	}
	for _, command := range plan.Entries[0].Suggestions {
		if command.Side == "current" { t.Fatal("current bad batch received extra repetitions") }
	}
	policy, scenarios, source = plannerPreflightTestInputs()
	policy.RequiredEnvironments[0].Expect.RequirePlatformVersion = true
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil, source)
	if err != nil || plan.Environments[0].Current.Action != "version_evidence_required" ||
		plan.Preflight.Resolutions[0].Status != "not_applicable" || len(plan.Entries[0].Suggestions) != 0 {
		t.Fatal("preflight manufactured required platform versions")
	}
}

func TestPlannerPreflightReaderBindsTargetFileAndReportBytes(t *testing.T) {
	// adr: 984 — a saved report must match the current explicit target mapping.
	_, scenarios, source := plannerPreflightTestInputs()
	dir := t.TempDir()
	targetsPath, reportPath := filepath.Join(dir, "targets.yaml"), filepath.Join(dir, "preflight.json")
	targets := "schema_version: 1\nscenarios:\n  checkout:\n    checkout: live-checkout\n"
	if err := os.WriteFile(targetsPath, []byte(targets), 0o600); err != nil { t.Fatal(err) }
	_, digest, err := readTestEnvironmentPreflightTargets(targetsPath, scenarios)
	if err != nil { t.Fatal(err) }
	source.Report.TargetsSHA256 = digest
	body, err := json.Marshal(source.Report)
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(reportPath, body, 0o600); err != nil { t.Fatal(err) }
	loaded, err := readTestPlannerPreflight(reportPath, targetsPath, scenarios, "current", 15*time.Minute, source.Now)
	if err != nil || len(loaded.ReportSHA256) != 64 || loaded.TargetsSHA256 != digest { t.Fatal("valid source report rejected") }
	if err := os.WriteFile(targetsPath, []byte(strings.ReplaceAll(targets, "live-checkout", "other-checkout")), 0o600); err != nil { t.Fatal(err) }
	if _, err := readTestPlannerPreflight(reportPath, targetsPath, scenarios, "current", 15*time.Minute, source.Now); err == nil {
		t.Fatal("different target mapping was accepted")
	}
}
