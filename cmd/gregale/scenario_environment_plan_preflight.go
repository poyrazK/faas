package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

type testPlannerPreflight struct {
	Report testEnvironmentPreflightReport
	ReportSHA256 string
	Targets testEnvironmentPreflightTargets
	TargetsSHA256 string
	Side string
	Now time.Time
	MaxAge time.Duration
}

type testPlanPreflightResolution struct {
	Requirement string `json:"requirement"`
	Configuration string `json:"configuration_sha256,omitempty"`
	Context testFailureGroupKey `json:"context"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	ResolvedFields []string `json:"resolved_fields"`
	ObservedAt time.Time `json:"oldest_observation_at"`
}

type testPlanPreflightSummary struct {
	EvaluatedAt time.Time `json:"evaluated_at"`
	EvidenceKind string `json:"evidence_kind"`
	ExecutionEvidence bool `json:"execution_evidence"`
	ReportSHA256 string `json:"report_sha256"`
	TargetsSHA256 string `json:"targets_sha256"`
	Side string `json:"side"`
	MaxAgeSeconds int64 `json:"max_age_seconds"`
	Resolutions []testPlanPreflightResolution `json:"resolutions"`
}

func readTestPlannerPreflight(path, targetsPath string, scenarios map[string]testScenario, side string, maxAge time.Duration, now time.Time) (*testPlannerPreflight, error) {
	file, err := openCustomerFile(path)
	if err != nil { return nil, err }
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil { return nil, err }
	if len(body) > 8<<20 { return nil, errors.New("preflight report exceeds 8 MiB") }
	var report testEnvironmentPreflightReport
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&report); err != nil { return nil, fmt.Errorf("decode preflight report: %w", err) }
	if decoder.Decode(new(any)) != io.EOF { return nil, errors.New("preflight report must contain one JSON object") }
	targets, targetsSHA, err := readTestEnvironmentPreflightTargets(targetsPath, scenarios)
	if err != nil { return nil, err }
	digest := sha256.Sum256(body)
	source := &testPlannerPreflight{Report: report, ReportSHA256: hex.EncodeToString(digest[:]), Targets: targets,
		TargetsSHA256: targetsSHA, Side: side, Now: now, MaxAge: maxAge}
	if err := validateTestPlannerPreflight(source); err != nil { return nil, err }
	return source, nil
}

func validateTestPlannerPreflight(source *testPlannerPreflight) error {
	if source.Side != "current" && source.Side != "baseline" { return errors.New("preflight side must be current or baseline") }
	if source.MaxAge < time.Second || source.MaxAge > time.Hour { return errors.New("preflight maximum age must be between 1s and 1h") }
	report := source.Report
	if report.SchemaVersion != 1 || report.EvidenceKind != "platform_preflight" || report.ExecutionEvidence || len(report.Requirements) == 0 {
		return errors.New("expected a non-execution version 1 platform_preflight report")
	}
	if source.Targets.SchemaVersion != 1 || source.TargetsSHA256 == "" || report.TargetsSHA256 != source.TargetsSHA256 {
		return errors.New("preflight target mapping hash does not match supplied targets")
	}
	if report.StartedAt.IsZero() || report.FinishedAt.Before(report.StartedAt) || report.FinishedAt.After(source.Now) ||
		source.Now.Sub(report.FinishedAt) > source.MaxAge {
		return errors.New("preflight timestamps are stale, future-dated or invalid")
	}
	names := make(map[string]bool)
	for _, row := range report.Requirements {
		if row.Name == "" || names[row.Name] || row.ExecutionContextVerified { return errors.New("preflight requirement identities are ambiguous or claim verified execution") }
		names[row.Name] = true
		workloads := make(map[string]bool)
		for _, workload := range row.Workloads {
			if workload.Name == "" || workloads[workload.Name] { return errors.New("preflight workload observations are ambiguous") }
			workloads[workload.Name] = true
			if workload.ReadStatus == "read" && (workload.ObservedAt.IsZero() || workload.ObservedAt.Before(report.StartedAt) ||
				workload.ObservedAt.After(report.FinishedAt) || workload.ObservedAt.After(source.Now) || source.Now.Sub(workload.ObservedAt) > source.MaxAge) {
				return errors.New("preflight workload observation is stale, future-dated or outside its read window")
			}
		}
	}
	return nil
}

func testPlannerPreflightSnapshot(source *testPlannerPreflight, requirement testRequiredEnvironment) (*testEnvironmentSnapshot, time.Time, error) {
	var recorded *testEnvironmentPreflightRequirement
	wanted, _ := json.Marshal(requirement.Match)
	for i := range source.Report.Requirements {
		row := &source.Report.Requirements[i]
		context, _ := json.Marshal(row.IntendedContext)
		if row.Name == requirement.Name && string(context) == string(wanted) { recorded = row }
	}
	if recorded == nil { return nil, time.Time{}, errors.New("no preflight observation matches the requirement name and exact selectors") }
	scenario := testRequiredSelectorValue(requirement.Match.Scenario)
	mapping := source.Targets.Scenarios[scenario]
	snapshot := &testEnvironmentSnapshot{SchemaVersion: 1, Workloads: make(map[string]testWorkloadEnvironment)}
	oldest := time.Time{}
	for _, workload := range recorded.Workloads {
		if workload.ReadStatus != "read" { continue }
		if mapping[workload.Name] == "" { return nil, oldest, errors.New("observed workload is not present in supplied scenario target mapping") }
		fields := make(map[string]json.RawMessage)
		for _, name := range []string{"type", "runtime", "runtime_version", "ram_mb", "vcpu", "cpu_millicores", "max_concurrency", "concurrency_per_vm", "idle_timeout_seconds", "request_timeout_seconds", "min_instances"} {
			if value, exists := workload.Fields[name]; exists { fields[name] = value }
		}
		body, err := json.Marshal(fields)
		if err != nil { return nil, oldest, err }
		var observed testWorkloadEnvironment
		if err := json.Unmarshal(body, &observed); err != nil { return nil, oldest, errors.New("preflight resource observation has invalid field types") }
		snapshot.Workloads[workload.Name] = observed
		if oldest.IsZero() || workload.ObservedAt.Before(oldest) { oldest = workload.ObservedAt }
	}
	for name := range requirement.Expect.Workloads {
		if _, exists := snapshot.Workloads[name]; !exists || mapping[name] == "" { return nil, oldest, fmt.Errorf("workload %q has no readable mapped preflight observation", name) }
	}
	return snapshot, oldest, nil
}

func applyTestPlannerPreflight(plan *testCampaignPolicyPlan, policy testCampaignPolicy, source *testPlannerPreflight) error {
	if err := validateTestPlannerPreflight(source); err != nil { return err }
	summary := &testPlanPreflightSummary{EvaluatedAt: source.Now, EvidenceKind: "platform_preflight", ReportSHA256: source.ReportSHA256,
		TargetsSHA256: source.TargetsSHA256, Side: source.Side, MaxAgeSeconds: int64(source.MaxAge/time.Second),
		Resolutions: make([]testPlanPreflightResolution, 0)}
	for index := range plan.Environments {
		entry := &plan.Environments[index]
		resolution := testPlanPreflightResolution{Requirement: entry.Requirement, Configuration: entry.Configuration, Context: entry.Context,
			Status: "not_applicable", ResolvedFields: make([]string, 0)}
		side := &entry.Current
		if source.Side == "baseline" { side = &entry.Baseline }
		if side.Action != "platform_evidence_required" {
			resolution.Reason = "preflight does not waive manifest, version, historical configuration or report replacement prerequisites"
			summary.Resolutions = append(summary.Resolutions, resolution)
			continue
		}
		var requirement *testRequiredEnvironment
		for i := range policy.RequiredEnvironments {
			if policy.RequiredEnvironments[i].Name == entry.Requirement { requirement = &policy.RequiredEnvironments[i] }
		}
		if requirement == nil { return errors.New("planner environment requirement is absent from policy") }
		snapshot, oldest, err := testPlannerPreflightSnapshot(source, *requirement)
		resolution.ObservedAt = oldest
		if err != nil {
			resolution.Status, resolution.Reason = "rejected", err.Error()
			summary.Resolutions = append(summary.Resolutions, resolution)
			continue
		}
		// Only API-observable prerequisites are eligible. Declaration-based values
		// still need execution snapshots; precise versions remain unsupported.
		expected := requirement.Expect
		expected.PlatformVersion, expected.RequirePlatformVersion = nil, false
		expected.Workloads = make(map[string]testWorkloadExpectation)
		for name, workload := range requirement.Expect.Workloads {
			workload.Postgres, workload.Fixture, workload.FailFirst, workload.TCPPorts = nil, nil, nil, nil
			workload.RuntimeVersion, workload.RequireRuntimeVersion = nil, false
			expected.Workloads[name] = workload
		}
		valid := true
		checkTestEnvironmentExpectation(snapshot, expected, func(_ string, want, got any, known bool) {
			left, _ := json.Marshal(want)
			right, _ := json.Marshal(got)
			valid = valid && known && string(left) == string(right)
		})
		if !valid {
			resolution.Status, resolution.Reason = "rejected", "preflight resource fields are incomplete or do not satisfy the current policy"
			summary.Resolutions = append(summary.Resolutions, resolution)
			continue
		}
		side.Action, side.Reason = "ready_preflight", "fresh mapped platform fields match; execution snapshots and coverage are still required"
		resolution.Status, resolution.Reason = "resolved", side.Reason
		resolution.ResolvedFields = append(resolution.ResolvedFields, entry.PlatformFields...)
		sort.Strings(resolution.ResolvedFields)
		entry.Status = entry.Baseline.Action
		if entry.Status != entry.Current.Action { entry.Status = "side_actions_required" }
		summary.Resolutions = append(summary.Resolutions, resolution)
	}
	plan.Preflight = summary
	return nil
}
