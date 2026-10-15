package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

type testEnvironmentPreflightClient interface {
	GetApp(context.Context, string) (api.AppResponse, error)
}

type testEnvironmentPreflightTargets struct {
	SchemaVersion int `yaml:"schema_version"`
	Scenarios map[string]map[string]string `yaml:"scenarios"`
}

type testEnvironmentPreflightCheck struct {
	Field string `json:"field"`
	Source string `json:"source"`
	Status string `json:"status"`
	Expected json.RawMessage `json:"expected"`
	Observed json.RawMessage `json:"observed"`
	Reason string `json:"reason,omitempty"`
}

type testEnvironmentPreflightWorkload struct {
	Name string `json:"name"`
	ReadStatus string `json:"read_status"`
	ObservedAt time.Time `json:"observed_at"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type testEnvironmentPreflightRequirement struct {
	Name string `json:"name"`
	IntendedContext testCampaignPolicySelector `json:"intended_context"`
	ExecutionContextVerified bool `json:"execution_context_verified"`
	Ready bool `json:"ready"`
	Checks []testEnvironmentPreflightCheck `json:"checks"`
	Workloads []testEnvironmentPreflightWorkload `json:"workloads"`
}

type testEnvironmentPreflightReport struct {
	SchemaVersion int `json:"schema_version"`
	EvidenceKind string `json:"evidence_kind"`
	ExecutionEvidence bool `json:"execution_evidence"`
	TargetsSHA256 string `json:"targets_sha256"`
	StartedAt time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Ready bool `json:"ready"`
	ReadErrors int `json:"read_errors"`
	Requirements []testEnvironmentPreflightRequirement `json:"requirements"`
}

func readTestEnvironmentPreflightTargets(path string, scenarios map[string]testScenario) (testEnvironmentPreflightTargets, string, error) {
	var targets testEnvironmentPreflightTargets
	file, err := openCustomerFile(path)
	if err != nil { return targets, "", err }
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil { return targets, "", err }
	if len(body) > 1<<20 { return targets, "", errors.New("preflight targets exceed 1 MiB") }
	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&targets); err != nil { return targets, "", err }
	if decoder.Decode(new(any)) != io.EOF { return targets, "", errors.New("targets must contain one YAML document") }
	if targets.SchemaVersion != 1 || len(targets.Scenarios) == 0 || len(targets.Scenarios) > 100 {
		return targets, "", errors.New("targets schema_version must be 1 with 1..100 scenarios")
	}
	for name, workloads := range targets.Scenarios {
		scenario, exists := scenarios[name]
		if !exists || len(workloads) > 32 { return targets, "", fmt.Errorf("invalid targets scenario %q", name) }
		seen := make(map[string]bool)
		for logical, slug := range workloads {
			_, service := scenario.Services[logical]
			if (logical != scenario.Project && !service) || !api.ValidAppSlug(slug) || seen[slug] {
				return targets, "", fmt.Errorf("invalid or aliased workload target in scenario %q", name)
			}
			seen[slug] = true
		}
	}
	digest := sha256.Sum256(body)
	return targets, hex.EncodeToString(digest[:]), nil
}

func cmdTestPolicyEnvironmentPreflight(args []string) int {
	fs := newFlagSet("test policy preflight", flag.ContinueOnError)
	path := fs.String("policy", "", "YAML campaign policy")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "scenario manifest")
	targetsPath := fs.String("targets", "", "logical workload to existing app slug mappings")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) { return 0 }
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *path == "" || *targetsPath == "" {
		return printErr("Invalid environment preflight options", errors.New("--policy and --targets are required"))
	}
	policy, err := readTestCampaignPolicy(*path)
	if err != nil { return printErr("Invalid policy", err) }
	if len(policy.RequiredEnvironments) == 0 {
		return printErr("Invalid environment preflight policy", errors.New("policy declares no required_environments"))
	}
	manifest, _, err := readTestManifestDocument(*manifestPath, func(_ string, scenario testScenario) []string { return testHTTPDataFields(scenario) })
	if err != nil { return printErr("Invalid scenario manifest", err) }
	targets, digest, err := readTestEnvironmentPreflightTargets(*targetsPath, manifest.Scenarios)
	if err != nil { return printErr("Invalid preflight targets", err) }
	client, err := authedClient()
	if err != nil { return printErr("Not logged in", err) }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report := inspectTestEnvironmentPreflight(ctx, client, policy, manifest.Scenarios, targets, digest)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil { return printErr("Could not write environment preflight", err) }
	if report.ReadErrors > 0 { return 1 }
	return 0
}

func testEnvironmentPreflightFields(observed testWorkloadEnvironment) map[string]json.RawMessage {
	body, _ := json.Marshal(observed)
	var all map[string]json.RawMessage
	_ = json.Unmarshal(body, &all)
	fields := make(map[string]json.RawMessage)
	// Explicit allowlist: dependency declarations and raw app metadata are not live observations.
	for _, name := range []string{"type", "runtime", "runtime_version", "ram_mb", "vcpu", "cpu_millicores", "max_concurrency", "concurrency_per_vm", "idle_timeout_seconds", "request_timeout_seconds", "min_instances"} {
		fields[name] = all[name]
	}
	return fields
}

func inspectTestEnvironmentPreflight(ctx context.Context, client testEnvironmentPreflightClient, policy testCampaignPolicy, scenarios map[string]testScenario, targets testEnvironmentPreflightTargets, digest string) testEnvironmentPreflightReport {
	report := testEnvironmentPreflightReport{SchemaVersion: 1, EvidenceKind: "platform_preflight", TargetsSHA256: digest,
		StartedAt: time.Now().UTC(), Ready: true, Requirements: make([]testEnvironmentPreflightRequirement, 0)}
	type cachedApp struct {
		App api.AppResponse
		ReadStatus string
		At time.Time
	}
	cache := make(map[string]cachedApp)
	for _, requirement := range policy.RequiredEnvironments {
		row := testEnvironmentPreflightRequirement{Name: requirement.Name, IntendedContext: requirement.Match, Ready: true,
			Checks: make([]testEnvironmentPreflightCheck, 0), Workloads: make([]testEnvironmentPreflightWorkload, 0)}
		add := func(field, source, status string, expected, observed any, reason string) {
			left, _ := json.Marshal(expected)
			right, _ := json.Marshal(observed)
			row.Checks = append(row.Checks, testEnvironmentPreflightCheck{Field: field, Source: source, Status: status, Expected: left, Observed: right, Reason: reason})
			if status != "matched" && status != "declared" { row.Ready = false }
		}
		name := testRequiredSelectorValue(requirement.Match.Scenario)
		scenario, exists := scenarios[name]
		if !exists {
			add("scenario", "manifest", "mismatch", name, nil, "required scenario is absent from manifest")
			report.Requirements = append(report.Requirements, row)
			report.Ready = false
			continue
		}
		if requirement.Match.Engine != nil && *requirement.Match.Engine != "real-vm" {
			add("engine", "capability", "unavailable", *requirement.Match.Engine, nil, "preflight reads native platform workload settings")
			report.Requirements = append(report.Requirements, row)
			report.Ready = false
			continue
		}
		if requirement.Match.Profile != nil {
			_, err := selectedTestProfiles(*requirement.Match.Profile)
			if err != nil || *requirement.Match.Profile == "all" {
				add("profile", "manifest", "mismatch", *requirement.Match.Profile, nil, "selector does not name a native lifecycle profile")
			}
		}
		if requirement.Match.Case != nil || requirement.Match.Configuration != nil {
			candidates, err := testCampaignPlanCandidates(scenario)
			matched := false
			if err == nil {
				for _, candidate := range candidates {
					matched = matched || ((requirement.Match.Case == nil || candidate.Case == *requirement.Match.Case) &&
						(requirement.Match.Configuration == nil || candidate.Hash == *requirement.Match.Configuration))
				}
			}
			if !matched { add("configuration_selector", "manifest", "mismatch", requirement.Match, nil, "case/configuration selector is absent from manifest candidates") }
		}
		checks, _, _ := testEnvironmentPlanDeclarations(scenario, requirement.Expect)
		for _, check := range checks {
			status := "declared"
			if check.Status == "change_required" { status = "mismatch" }
			add(check.Field, "manifest", status, check.Expected, check.Declared, "declaration only; runtime behavior is not verified")
		}
		needed := map[string]bool{scenario.Project: true}
		for logical := range requirement.Expect.Workloads { needed[logical] = true }
		names := make([]string, 0, len(needed))
		for logical := range needed { names = append(names, logical) }
		sort.Strings(names)
		snapshot := &testEnvironmentSnapshot{SchemaVersion: 1, Workloads: make(map[string]testWorkloadEnvironment)}
		readable := make(map[string]bool)
		for _, logical := range names {
			observation := testEnvironmentPreflightWorkload{Name: logical, ReadStatus: "unmapped", Fields: make(map[string]json.RawMessage)}
			slug := targets.Scenarios[name][logical]
			if slug != "" {
				cached, ok := cache[slug]
				if !ok {
					cached.ReadStatus = "read_error"
					if ctx.Err() == nil {
						app, err := client.GetApp(ctx, slug)
						if err == nil {
							cached.App, cached.ReadStatus = app, "read"
							if app.Status == "deleted" { cached.ReadStatus = "unavailable" }
							if app.Slug != slug { cached.ReadStatus = "identity_mismatch" }
						}
					}
					cached.At = time.Now().UTC()
					cache[slug] = cached
					if cached.ReadStatus == "read_error" || cached.ReadStatus == "identity_mismatch" { report.ReadErrors++ }
				}
				observation.ReadStatus, observation.ObservedAt = cached.ReadStatus, cached.At
				if cached.ReadStatus == "read" {
					observed := testEnvironmentFromApp(cached.App, testService{})
					snapshot.Workloads[logical] = observed
					readable[logical] = true
					observation.Fields = testEnvironmentPreflightFields(observed)
				}
			}
			if !readable[logical] { add("workloads."+logical+".read", "platform", "unavailable", "readable existing app", nil, "target is unmapped, unavailable or unreadable; raw API errors are omitted") }
			row.Workloads = append(row.Workloads, observation)
		}
		live := requirement.Expect
		live.Workloads = make(map[string]testWorkloadExpectation)
		for logical, want := range requirement.Expect.Workloads {
			want.Postgres, want.Fixture, want.FailFirst, want.TCPPorts = nil, nil, nil, nil
			live.Workloads[logical] = want
		}
		seen := make(map[string]bool)
		checkLive := func(field string, expected, observed any, known bool) {
			seen[field] = true
			for logical := range live.Workloads {
				if field == "workloads."+logical+".present" && !readable[logical] { known = false }
			}
			left, _ := json.Marshal(expected)
			right, _ := json.Marshal(observed)
			status, reason := "matched", ""
			if !known { status, reason, observed = "unavailable", "required metadata is not exposed or target could not be inspected", nil
			} else if string(left) != string(right) { status, reason = "mismatch", "live platform value differs from requirement" }
			add(field, "platform", status, expected, observed, reason)
		}
		checkTestEnvironmentExpectation(snapshot, live, checkLive)
		for _, logical := range names {
			if _, expected := live.Workloads[logical]; expected && !seen["workloads."+logical+".present"] {
				checkLive("workloads."+logical+".present", true, readable[logical], readable[logical])
			}
		}
		for _, logical := range names {
			want, expected := requirement.Expect.Workloads[logical]
			if !expected { continue }
			for _, field := range []struct{name string; value any; requested bool}{
				{"postgres", want.Postgres, want.Postgres != nil}, {"fixture", want.Fixture, want.Fixture != nil},
				{"fail_first", want.FailFirst, want.FailFirst != nil}, {"tcp_ports", want.TCPPorts, want.TCPPorts != nil},
			} {
				if field.requested { add("workloads."+logical+"."+field.name, "platform", "unavailable", field.value, nil, "not exposed by app resource reads; manifest declaration is shown separately") }
			}
		}
		report.Ready = report.Ready && row.Ready
		report.Requirements = append(report.Requirements, row)
	}
	report.FinishedAt = time.Now().UTC()
	return report
}
