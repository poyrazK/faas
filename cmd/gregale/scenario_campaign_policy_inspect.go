package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

type testCampaignPolicyDiagnostic struct {
	Code       string `json:"code"`
	Rule       string `json:"rule"`
	EarlierRule string `json:"earlier_rule,omitempty"`
	Message    string `json:"message"`
}

type testCampaignPolicyUsage struct {
	Rule     string `json:"rule"`
	Matches  int    `json:"observed_matches"`
	Selected int    `json:"observed_selections"`
}

type testCampaignPolicyInspection struct {
	RequiredEnvironments []testRequiredEnvironmentResult `json:"required_environments,omitempty"`
	RequiredCoverage []testCampaignRequiredCoverageResult `json:"required_coverage,omitempty"`
	SchemaVersion int                            `json:"schema_version"`
	Informational bool                           `json:"informational"`
	ObservationsSupplied bool                    `json:"observations_supplied"`
	Diagnostics   []testCampaignPolicyDiagnostic `json:"diagnostics"`
	Usage         []testCampaignPolicyUsage      `json:"rule_usage"`
	Preview       *testCampaignPolicyResult      `json:"prospective_result,omitempty"`
}

func cmdTestPolicy(args []string) int {
	if len(args)>0 && args[0]=="recover" {return cmdTestCampaignRecover(args[1:])}
	if len(args)>0 && (args[0]=="cancel" || args[0]=="cleanup") { return cmdTestCampaignControl(args[0],args[1:]) }
	if len(args)>0 && args[0]=="bundle" { return cmdTestCampaignBundle(args[1:]) }
	if len(args) > 0 && args[0] == "status" { return cmdTestCampaignStatus(args[1:]) }
	if len(args) > 0 && args[0] == "reconcile" { return cmdTestCampaignReconcile(args[1:]) }
	if len(args) > 0 && args[0] == "run" { return cmdTestCampaignRun(args[1:]) }
	if len(args) > 0 && args[0] == "preflight" {
		return cmdTestPolicyEnvironmentPreflight(args[1:])
	}
	if len(args) > 0 && args[0] == "plan" {
		return cmdTestPolicyPlan(args[1:])
	}
	if len(args) == 0 || (args[0] != "lint" && args[0] != "preview") {
		return printErr("Invalid policy command", errors.New("use test policy lint, preview, plan, preflight, run, reconcile, status, bundle, cancel, cleanup or recover"))
	}
	mode := args[0]
	fs := newFlagSet("test policy "+mode, flag.ContinueOnError)
	path := fs.String("policy", "", "YAML campaign policy")
	from := fs.String("from", "", "optional current raw JSON report (required for preview)")
	baseline := fs.String("baseline", "", "optional baseline raw JSON report (required for preview)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *path == "" ||
		(*baseline != "" && *from == "") || (mode == "preview" && (*from == "" || *baseline == "")) {
		return printErr("Invalid policy inspection options", errors.New("--policy is required; --baseline requires --from; preview requires both reports"))
	}
	policy, err := readTestCampaignPolicy(*path)
	if err != nil {
		return printErr("Invalid policy", err)
	}
	var before, after []testRunReceipt
	if *from != "" {
		after, _, err = readTestBaselineReport(*from, nil)
		if err != nil {
			return printErr("Could not read current report", err)
		}
	}
	if *baseline != "" {
		before, _, err = readTestBaselineReport(*baseline, nil)
		if err != nil {
			return printErr("Could not read baseline", err)
		}
	}
	inspection := inspectTestCampaignPolicy(policy, before, after, *from != "")
	if *baseline != "" {
		inspection.RequiredEnvironments = evaluateTestRequiredEnvironments(policy, before, after)
		for _, requirement := range inspection.RequiredEnvironments {
			if !requirement.Passed {
				inspection.Diagnostics = append(inspection.Diagnostics, testCampaignPolicyDiagnostic{
					Code: "required_environment_failed", Rule: requirement.Name, Message: requirement.Reason,
				})
			}
		}
		inspection.RequiredCoverage = evaluateTestCampaignRequiredCoverage(policy, before, after)
		for _, coverage := range inspection.RequiredCoverage {
			if !coverage.Passed {
				inspection.Diagnostics = append(inspection.Diagnostics, testCampaignPolicyDiagnostic{
					Code: "required_coverage_missing", Rule: coverage.Name, Message: coverage.Reason,
				})
			}
		}
	}
	if mode == "preview" {
		result, err := evaluateTestCampaignPolicy(policy, before, after, "", "")
		if err != nil {
			return printErr("Could not preview policy", err)
		}
		inspection.Preview = &result
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(inspection); err != nil {
		return printErr("Could not write policy inspection", err)
	}
	// Warnings and prospective policy rejections never turn inspection into a gate.
	return 0
}

func testCampaignSelectorFields(m testCampaignPolicySelector) []*string {
	return []*string{m.Configuration, m.Scenario, m.Profile, m.Engine, m.Case, m.Step, m.Phase, m.Kind, m.Assertion, m.Request}
}

func testCampaignSelectorContains(earlier, later testCampaignPolicySelector) bool {
	a, b := testCampaignSelectorFields(earlier), testCampaignSelectorFields(later)
	for i := range a {
		if a[i] != nil && (b[i] == nil || *a[i] != *b[i]) {
			return false
		}
	}
	return true
}

func inspectTestCampaignPolicy(policy testCampaignPolicy, before, after []testRunReceipt, observed bool) testCampaignPolicyInspection {
	inspection := testCampaignPolicyInspection{
		SchemaVersion: 1, Informational: true, ObservationsSupplied: observed,
		Diagnostics: make([]testCampaignPolicyDiagnostic, 0), Usage: make([]testCampaignPolicyUsage, 0),
	}
	type observation struct {
		key testFailureGroupKey
		hash string
		contextOnly bool
	}
	var observations []observation
	if observed {
		// Assertion observations are first-failure groups, just as in policy evaluation.
		for _, row := range compareTestDigestGroups(before, after) {
			observations = append(observations, observation{key: row.key, hash: row.fingerprint})
		}
		seen := make(map[testCampaignCoverageKey]bool)
		for _, receipts := range [][]testRunReceipt{before, after} {
			for _, receipt := range receipts {
				var leaves []testRunReceipt
				collectTestFailureLeaves(receipt, "", &leaves)
				for _, leaf := range leaves {
					hash := testDigestScopeFingerprint([]testRunReceipt{leaf})
					context := testCampaignCoverageKey{Hash: hash, Scope: testFailureScope(leaf)}
					if seen[context] {
						continue
					}
					seen[context] = true
					observations = append(observations, observation{
						key: testFailureGroupKey{Scenario: leaf.Scenario, Profile: leaf.Profile, Engine: leaf.Engine, Case: leaf.Case},
						hash: hash, contextOnly: true,
					})
				}
			}
		}
	}
	for index, rule := range policy.Rules {
		for _, earlier := range policy.Rules[:index] {
			if testCampaignSelectorContains(earlier.Match, rule.Match) {
				inspection.Diagnostics = append(inspection.Diagnostics, testCampaignPolicyDiagnostic{
					Code: "shadowed_rule", Rule: rule.Name, EarlierRule: earlier.Name,
					Message: "an earlier rule matches every selector combination covered by this rule",
				})
				break
			}
		}
		usage := testCampaignPolicyUsage{Rule: rule.Name}
		if observed {
			single := policy
			single.Rules = []testCampaignScopedRule{rule}
			for _, o := range observations {
				_, matched := selectTestCampaignPolicy(single, o.key, o.hash, o.contextOnly)
				if matched != rule.Name {
					continue
				}
				usage.Matches++
				_, selected := selectTestCampaignPolicy(policy, o.key, o.hash, o.contextOnly)
				if selected == rule.Name {
					usage.Selected++
				}
			}
			if usage.Matches == 0 {
				inspection.Diagnostics = append(inspection.Diagnostics, testCampaignPolicyDiagnostic{
					Code: "no_observed_match", Rule: rule.Name,
					Message: "no observed first-failure group or eligible context matches; this does not prove the selector is invalid or unused in future runs",
				})
			} else if usage.Selected == 0 {
				inspection.Diagnostics = append(inspection.Diagnostics, testCampaignPolicyDiagnostic{
					Code: "never_selected", Rule: rule.Name,
					Message: fmt.Sprintf("all %d observed matches select earlier rules", usage.Matches),
				})
			}
		}
		inspection.Usage = append(inspection.Usage, usage)
	}
	return inspection
}
