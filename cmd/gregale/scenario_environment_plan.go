package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

type testEnvironmentPlanCheck struct {
	Field string `json:"field"`
	Source string `json:"source"`
	Status string `json:"status"`
	Expected json.RawMessage `json:"expected"`
	Declared json.RawMessage `json:"declared"`
}

type testEnvironmentPlanSide struct {
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	ReportReplacementRequired bool `json:"report_replacement_required"`
}

type testEnvironmentPlanEntry struct {
	AllProfiles bool `json:"all_profiles,omitempty"`
	Requirement string `json:"requirement"`
	Configuration string `json:"configuration_sha256,omitempty"`
	Context testFailureGroupKey `json:"context"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	ManifestChecks []testEnvironmentPlanCheck `json:"manifest_checks"`
	PlatformFields []string `json:"platform_fields"`
	VersionFields []string `json:"version_fields"`
	Baseline testEnvironmentPlanSide `json:"baseline"`
	Current testEnvironmentPlanSide `json:"current"`
	Evidence *testRequiredEnvironmentResult `json:"evidence,omitempty"`
}

type testEnvironmentPlanBlocker struct {
	Requirement string `json:"requirement"`
	Side string `json:"side"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func testEnvironmentPlanDeclarations(scenario testScenario, expected testEnvironmentExpectation) ([]testEnvironmentPlanCheck, []string, []string) {
	checks := make([]testEnvironmentPlanCheck, 0)
	platform, versions := make([]string, 0), make([]string, 0)
	check := func(field string, want, declared any, exists bool) {
		left, _ := json.Marshal(want)
		right, _ := json.Marshal(declared)
		status := "declared"
		if !exists || string(left) != string(right) {
			status = "change_required"
		}
		checks = append(checks, testEnvironmentPlanCheck{Field: field, Source: "manifest", Status: status, Expected: left, Declared: right})
	}
	if expected.PlatformVersion != nil || expected.RequirePlatformVersion {
		versions = append(versions, "platform_version")
	}
	names := make([]string, 0, len(expected.Workloads))
	for name := range expected.Workloads { names = append(names, name) }
	sort.Strings(names)
	for _, name := range names {
		want := expected.Workloads[name]
		spec, exists := scenario.Services[name]
		if name == scenario.Project {
			exists = true
			spec = testService{Postgres: scenario.Postgres, TCPPorts: scenario.TCPPorts}
		}
		prefix := "workloads." + name + "."
		check(prefix+"present", true, exists, true)
		if want.Postgres != nil { check(prefix+"postgres", want.Postgres, spec.Postgres, exists) }
		if want.Fixture != nil { check(prefix+"fixture", want.Fixture, spec.Fixture, exists) }
		if want.FailFirst != nil { check(prefix+"fail_first", want.FailFirst, spec.FailFirst, exists) }
		if want.TCPPorts != nil {
			ports, declared := append([]int{}, (*want.TCPPorts)...), append([]int{}, spec.TCPPorts...)
			sort.Ints(ports)
			sort.Ints(declared)
			check(prefix+"tcp_ports", ports, declared, exists)
		}
		if want.Type != nil { platform = append(platform, prefix+"type") }
		if want.Runtime != nil { platform = append(platform, prefix+"runtime") }
		if want.RAMMB != nil { platform = append(platform, prefix+"ram_mb") }
		if want.VCPU != nil { platform = append(platform, prefix+"vcpu") }
		if want.CPUMillicores != nil { platform = append(platform, prefix+"cpu_millicores") }
		if want.MaxConcurrency != nil { platform = append(platform, prefix+"max_concurrency") }
		if want.ConcurrencyPerVM != nil { platform = append(platform, prefix+"concurrency_per_vm") }
		if want.MinInstances != nil { platform = append(platform, prefix+"min_instances") }
		if want.RuntimeVersion != nil || want.RequireRuntimeVersion { versions = append(versions, prefix+"runtime_version") }
	}
	sort.Strings(platform)
	sort.Strings(versions)
	return checks, platform, versions
}

func testEnvironmentPlanDisposition(checks []testEnvironmentPlanCheck, platform, versions []string, evidence *testRequiredEnvironmentSide) testEnvironmentPlanSide {
	side := testEnvironmentPlanSide{}
	if evidence != nil {
		side.ReportReplacementRequired = evidence.ExecutedRuns > 0 && evidence.SatisfiedRuns < evidence.ExecutedRuns
	}
	for _, check := range checks {
		if check.Status == "change_required" {
			side.Action, side.Reason = "manifest_change_required", "correct declared workload/dependency settings before collecting more runs"
			return side
		}
	}
	if side.ReportReplacementRequired {
		side.Action, side.Reason = "report_evidence_required", "replace noncompliant receipts after correcting or collecting the required evidence; extra runs cannot remove existing failures"
		for _, issue := range evidence.Issues {
			if issue.Code == "mismatch" {
				side.Action, side.Reason = "platform_change_required", "correct recorded resource/runtime/dependency differences, then replace the noncompliant report batch"
				break
			}
		}
		for _, issue := range evidence.Issues {
			for _, field := range versions {
				if issue.Field == field || issue.Field == field+".known" {
					side.Action, side.Reason = "version_evidence_required", "required binary-version evidence is unavailable or incorrect; current native capture cannot collect it"
				}
			}
		}
		return side
	}
	if evidence != nil && evidence.ExecutedRuns > 0 && evidence.SatisfiedRuns == evidence.ExecutedRuns {
		side.Action = "ready"
		if len(versions) > 0 {
			side.Action, side.Reason = "ready_existing_evidence", "existing receipts satisfy versions, but current native capture cannot collect version evidence for additional runs"
		}
		return side
	}
	if len(versions) > 0 {
		side.Action, side.Reason = "version_evidence_required", "current native capture does not report precise platform/runtime binary versions"
	} else if len(platform) > 0 {
		side.Action, side.Reason = "platform_evidence_required", "confirm requested resources/runtime against platform observations before scheduling a campaign"
	} else {
		side.Action, side.Reason = "collect_execution_evidence", "manifest declarations match; execution still needs to record compliant snapshots"
	}
	return side
}

func buildTestEnvironmentPlan(policy testCampaignPolicy, scenarios map[string]testScenario, before, after []testRunReceipt) ([]testEnvironmentPlanEntry, error) {
	entries := make([]testEnvironmentPlanEntry, 0)
	observed := evaluateTestRequiredEnvironments(policy, before, after)
	for _, requirement := range policy.RequiredEnvironments {
		name := testRequiredSelectorValue(requirement.Match.Scenario)
		base := testEnvironmentPlanEntry{Requirement: requirement.Name,
			Context: testFailureGroupKey{Scenario: name, Profile: testRequiredSelectorValue(requirement.Match.Profile), Engine: "real-vm", Case: testRequiredSelectorValue(requirement.Match.Case)},
			Configuration: testRequiredSelectorValue(requirement.Match.Configuration),
			ManifestChecks: make([]testEnvironmentPlanCheck, 0), PlatformFields: make([]string, 0), VersionFields: make([]string, 0)}
		blocked := func(status, reason string) {
			entry := base
			entry.Status, entry.Reason = status, reason
			entry.Baseline, entry.Current = testEnvironmentPlanSide{Action: status, Reason: reason}, testEnvironmentPlanSide{Action: status, Reason: reason}
			entries = append(entries, entry)
		}
		if requirement.Match.Engine != nil && *requirement.Match.Engine != "real-vm" {
			base.Context.Engine = *requirement.Match.Engine
			blocked("unsupported", "environment planning uses native real-vm snapshots")
			continue
		}
		scenario, exists := scenarios[name]
		if !exists {
			blocked("manifest_change_required", "required scenario is absent from manifest")
			continue
		}
		profiles := []string{"warm", "cold", "restored"}
		if requirement.Match.Profile != nil {
			selected, err := selectedTestProfiles(*requirement.Match.Profile)
			if err != nil || *requirement.Match.Profile == "all" {
				base.AllProfiles = true
				blocked("manifest_change_required", "profile selector does not name one native lifecycle profile")
				continue
			}
			profiles = selected
		}
		candidates, err := testCampaignPlanCandidates(scenario)
		if err != nil { return nil, err }
		matched := make(map[testCampaignCoverageKey]bool)
		count := 0
		for _, candidate := range candidates {
			if requirement.Match.Case != nil && *requirement.Match.Case != candidate.Case { continue }
			if requirement.Match.Configuration != nil && *requirement.Match.Configuration != candidate.Hash { continue }
			for _, profile := range profiles {
				count++
				entry := base
				entry.Configuration, entry.Context.Profile, entry.Context.Case = candidate.Hash, profile, candidate.Case
				entry.ManifestChecks, entry.PlatformFields, entry.VersionFields = testEnvironmentPlanDeclarations(candidate.Scenario, requirement.Expect)
				key := testCampaignCoverageKey{Hash: candidate.Hash, Scope: testFailureGroupScope{scenario: name, profile: profile, engine: "real-vm", caseName: candidate.Case}}
				matched[key] = true
				for i := range observed {
					r := &observed[i]
					if r.Name == requirement.Name && r.Configuration == candidate.Hash && r.Context.Profile == profile &&
						r.Context.Engine == "real-vm" && r.Context.Case == candidate.Case {
						entry.Evidence = r
					}
				}
				var oldSide, currentSide *testRequiredEnvironmentSide
				if entry.Evidence != nil { oldSide, currentSide = &entry.Evidence.Baseline, &entry.Evidence.Current }
				entry.Baseline = testEnvironmentPlanDisposition(entry.ManifestChecks, entry.PlatformFields, entry.VersionFields, oldSide)
				entry.Current = testEnvironmentPlanDisposition(entry.ManifestChecks, entry.PlatformFields, entry.VersionFields, currentSide)
				entry.Status = entry.Baseline.Action
				if entry.Current.Action != entry.Status { entry.Status = "side_actions_required" }
				entries = append(entries, entry)
			}
		}
		if count == 0 { blocked("unavailable_configuration", "required case or configuration fingerprint is absent from manifest candidates") }
		for i := range observed {
			r := &observed[i]
			if r.Name != requirement.Name || (r.Baseline.ExecutedRuns == 0 && r.Current.ExecutedRuns == 0) { continue }
			key := testCampaignCoverageKey{Hash: r.Configuration, Scope: testFailureGroupScope{scenario: r.Context.Scenario, profile: r.Context.Profile, engine: r.Context.Engine, caseName: r.Context.Case}}
			if !matched[key] {
				entry := base
				entry.Configuration, entry.Context, entry.Evidence = r.Configuration, r.Context, r
				entry.Status, entry.Reason = "unavailable_configuration", "historical environment context requires its original manifest"
				entry.Baseline, entry.Current = testEnvironmentPlanSide{Action: entry.Status, Reason: entry.Reason}, testEnvironmentPlanSide{Action: entry.Status, Reason: entry.Reason}
				entries = append(entries, entry)
			}
		}
	}
	return entries, nil
}

// A scenario command executes every matrix case for its profile. A prerequisite
// on any case therefore also applies to commands suggested for sibling cases.
func applyTestEnvironmentPlan(plan *testCampaignPolicyPlan) {
	for index := range plan.Entries {
		entry := &plan.Entries[index]
		entry.EnvironmentBlockers = make([]testEnvironmentPlanBlocker, 0)
		blockedSides := make(map[string]bool)
		for _, environment := range plan.Environments {
			if environment.Context.Scenario != entry.Context.Scenario || environment.Context.Engine != entry.Context.Engine ||
				(!environment.AllProfiles && environment.Context.Profile != "" && environment.Context.Profile != entry.Context.Profile) { continue }
			for _, side := range []struct{name string; value testEnvironmentPlanSide}{{"baseline", environment.Baseline}, {"current", environment.Current}} {
				if side.value.Action == "ready" || side.value.Action == "ready_preflight" || side.value.Action == "collect_execution_evidence" { continue }
				if side.value.Action == "ready_existing_evidence" {
					needsMore := false
					for _, command := range entry.Suggestions { needsMore = needsMore || command.Side == side.name }
					if !needsMore { continue }
				}
				blockedSides[side.name] = true
				entry.EnvironmentBlockers = append(entry.EnvironmentBlockers, testEnvironmentPlanBlocker{
					Requirement: environment.Requirement, Side: side.name, Action: side.value.Action, Reason: side.value.Reason,
				})
			}
		}
		if len(blockedSides) == 0 { continue }
		commands := make([]testCampaignPlanCommand, 0)
		for _, command := range entry.Suggestions {
			if !blockedSides[command.Side] { commands = append(commands, command) }
		}
		entry.Suggestions = commands
		if entry.Status == "needs_runs" || entry.Status == "covered" {
			entry.Status = "environment_blocked"
			if len(commands) > 0 { entry.Status = "partially_environment_blocked" }
			entry.Reason = fmt.Sprintf("%d environment prerequisites block suggested batches; see environment_blockers", len(entry.EnvironmentBlockers))
		}
	}
}
