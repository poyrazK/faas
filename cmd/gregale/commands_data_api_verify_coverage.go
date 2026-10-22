package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

type dataAPICoverageMapping struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Scenario string `json:"scenario,omitempty"`
}
type dataAPICoverageRule struct {
	Relation string                   `json:"relation"`
	Required []string                 `json:"required"`
	Checks   []dataAPICoverageMapping `json:"checks"`
}
type dataAPICoveragePolicy struct {
	Version         int                   `json:"version"`
	DefaultRequired []string              `json:"default_required"`
	Relations       []dataAPICoverageRule `json:"relations"`
}
type dataAPICoverageItem struct {
	Relation string   `json:"relation"`
	New      bool     `json:"new_to_schema,omitempty"`
	Verified []string `json:"verified"`
	Missing  []string `json:"missing"`
}
type dataAPICoverageReport struct {
	Version     int                   `json:"version"`
	Command     string                `json:"command"`
	Status      string                `json:"status"`
	Fingerprint string                `json:"schema_fingerprint"`
	Relations   []dataAPICoverageItem `json:"relations"`
	Gaps        int                   `json:"gaps"`
}
type dataAPICoverageEvidence struct {
	Version       int    `json:"version"`
	Command       string `json:"command"`
	Status        string `json:"status"`
	Cleanup       string `json:"cleanup"`
	Fingerprint   string `json:"schema_fingerprint"`
	Authorization string `json:"authorization"`
	Checks        []struct {
		Name   string `json:"name"`
		Passed bool   `json:"passed"`
	} `json:"checks"`
	Access []struct {
		Name     string `json:"name"`
		Scenario string `json:"scenario"`
		Passed   bool   `json:"passed"`
		Skipped  bool   `json:"skipped"`
	} `json:"access_checks"`
	Budget *struct {
		Status     string            `json:"status"`
		Checked    int               `json:"checked"`
		Violations []json.RawMessage `json:"violations"`
	} `json:"budget_check"`
	Results []struct {
		Name     string `json:"name"`
		Scenario string `json:"scenario"`
		Status   string `json:"status"`
		Relation string `json:"relation"`
	} `json:"results"`
}

var coverageKinds = map[string]bool{"integrity": true, "access_read": true, "access_mutation": true, "access_admin": true, "access_revocation": true, "performance": true}

func coveragePolicy(path string) (dataAPICoveragePolicy, error) {
	var policy dataAPICoveragePolicy
	f, err := openCustomerFile(path)
	if err != nil {
		return policy, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return policy, errors.New("policy exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF || policy.Version != 1 || len(policy.Relations) > 1000 || len(policy.DefaultRequired) == 0 {
		return policy, errors.New("use a version 1 coverage policy with explicit default_required rules")
	}
	validRequired := func(kinds []string) bool {
		seen := map[string]bool{}
		for _, kind := range kinds {
			if !coverageKinds[kind] || seen[kind] {
				return false
			}
			seen[kind] = true
		}
		return true
	}
	if !validRequired(policy.DefaultRequired) {
		return policy, errors.New("invalid default coverage requirements")
	}
	seen := map[string]bool{}
	count := 0
	for _, rule := range policy.Relations {
		if rule.Relation == "" || seen[rule.Relation] || rule.Required == nil || !validRequired(rule.Required) {
			return policy, errors.New("relations require unique names and explicit required arrays")
		}
		seen[rule.Relation] = true
		mappings := map[string]bool{}
		for _, mapping := range rule.Checks {
			count++
			key := mapping.Kind + "\x00" + mapping.Scenario + "\x00" + mapping.Name
			if count > 1000 || !coverageKinds[mapping.Kind] || strings.TrimSpace(mapping.Name) == "" || len(mapping.Name) > 200 || len(mapping.Scenario) > 200 || mappings[key] || (mapping.Kind == "integrity" && mapping.Scenario != "") || (mapping.Kind != "integrity" && mapping.Scenario == "") {
				return policy, errors.New("invalid or duplicate named check mapping")
			}
			mappings[key] = true
		}
	}
	return policy, nil
}
func readCoverageEvidence(path, command, fingerprint string) (*dataAPICoverageEvidence, error) {
	if path == "" {
		return nil, nil
	}
	var report dataAPICoverageEvidence
	if err := verificationJSON(path, &report, 1048576); err != nil {
		return nil, err
	}
	if report.Version != 1 || report.Command != command || report.Fingerprint != fingerprint || report.Cleanup != "passed" || !containsString([]string{"passed", "failed"}, report.Status) {
		return nil, errors.New("evidence must be a cleaned-up report matching the current schema; rerun verification or explain")
	}
	if len(report.Checks) > 100 || len(report.Access) > 100 || len(report.Results) > 100 {
		return nil, errors.New("evidence exceeds supported check bounds")
	}
	if report.Status == "passed" && command == "data-api verify" {
		if report.Authorization != "passed" {
			return nil, errors.New("verification evidence has no passing authorization result")
		}
		for _, check := range report.Checks {
			if !check.Passed {
				return nil, errors.New("passed verification report contains failed checks")
			}
		}
		for _, check := range report.Access {
			if !check.Passed || check.Skipped {
				return nil, errors.New("passed verification report contains failed or skipped access checks")
			}
		}
	}
	if report.Status == "passed" && command == "data-api explain" {
		planned := 0
		for _, query := range report.Results {
			if query.Status == "planned" {
				planned++
			} else if query.Status != "skipped" {
				return nil, errors.New("passed plan report contains failed queries")
			}
		}
		if report.Budget != nil && report.Budget.Status == "passed" && (report.Budget.Checked != planned || planned == 0 || len(report.Budget.Violations) > 0) {
			return nil, errors.New("budget coverage does not match planned reads")
		}
	}
	// Duplicate names make evidence ambiguous, even if one duplicate passed.
	seen := map[string]bool{}
	for _, check := range report.Checks {
		key := "sql\x00" + check.Name
		if check.Name == "" || seen[key] {
			return nil, errors.New("ambiguous integrity evidence")
		}
		seen[key] = true
	}
	for _, check := range report.Access {
		key := "access\x00" + check.Scenario + "\x00" + check.Name
		if check.Name == "" || check.Scenario == "" || seen[key] {
			return nil, errors.New("ambiguous access evidence")
		}
		seen[key] = true
	}
	for _, query := range report.Results {
		key := "query\x00" + query.Scenario + "\x00" + query.Name
		if query.Name == "" || query.Scenario == "" || seen[key] {
			return nil, errors.New("ambiguous query evidence")
		}
		seen[key] = true
	}
	return &report, nil
}
func evaluateDataAPICoverage(contract dataAPIContract, policy dataAPICoveragePolicy, verification, plans *dataAPICoverageEvidence, previous *dataAPIContract) (dataAPICoverageReport, error) {
	report := dataAPICoverageReport{Version: 1, Command: "data-api verify coverage", Status: "passed", Fingerprint: contract.Fingerprint, Relations: []dataAPICoverageItem{}}
	relations := map[string]bool{}
	for _, table := range contract.Parsed.Tables {
		relations[table.Schema+"."+table.Name] = true
	}
	rules := map[string]dataAPICoverageRule{}
	for _, rule := range policy.Relations {
		if !relations[rule.Relation] {
			return report, errors.New("policy references a relation absent from the current schema")
		}
		rules[rule.Relation] = rule
	}
	old := map[string]bool{}
	if previous != nil {
		for _, table := range previous.Parsed.Tables {
			old[table.Schema+"."+table.Name] = true
		}
	}
	names := []string{}
	for relation := range relations {
		names = append(names, relation)
	}
	sort.Strings(names)
	for _, relation := range names {
		rule, declared := rules[relation]
		required := policy.DefaultRequired
		if declared {
			required = rule.Required
		}
		item := dataAPICoverageItem{Relation: relation, New: previous != nil && !old[relation], Verified: []string{}, Missing: []string{}}
		for _, kind := range required {
			found := false
			allPassed := true
			for _, mapping := range rule.Checks {
				if mapping.Kind != kind {
					continue
				}
				found = true
				passed := false
				if kind == "integrity" && verification != nil && verification.Status == "passed" && verification.Authorization == "passed" {
					for _, check := range verification.Checks {
						if check.Name == mapping.Name {
							passed = check.Passed
						}
					}
				}
				if strings.HasPrefix(kind, "access_") && verification != nil && verification.Status == "passed" && verification.Authorization == "passed" {
					for _, check := range verification.Access {
						if check.Name == mapping.Name && check.Scenario == mapping.Scenario {
							passed = check.Passed && !check.Skipped
						}
					}
				}
				if kind == "performance" && plans != nil && plans.Status == "passed" && plans.Budget != nil && plans.Budget.Status == "passed" && plans.Budget.Checked > 0 && len(plans.Budget.Violations) == 0 {
					for _, query := range plans.Results {
						if query.Name == mapping.Name && query.Scenario == mapping.Scenario && query.Relation == relation {
							passed = query.Status == "planned"
						}
					}
				}
				allPassed = allPassed && passed
			}
			if found && allPassed {
				item.Verified = append(item.Verified, kind)
			} else {
				item.Missing = append(item.Missing, kind)
				report.Gaps++
			}
		}
		report.Relations = append(report.Relations, item)
	}
	if report.Gaps > 0 {
		report.Status = "missing_coverage"
	}
	return report, nil
}
func cmdDataAPIVerifyCoverage(args []string) int {
	fs := newFlagSet("data-api verify coverage", flag.ContinueOnError)
	schema := fs.String("schema", "", "current saved schema contract")
	policyPath := fs.String("policy", "", "explicit coverage policy")
	verificationPath := fs.String("verification", "", "verify JSON report")
	plansPath := fs.String("plans", "", "budget-checked explain JSON report")
	baseline := fs.String("baseline-schema", "", "previous schema contract")
	check := fs.Bool("check", false, "fail on missing required coverage")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *schema == "" || *policyPath == "" {
		return printErr("Invalid coverage options", errors.New("provide --schema FILE --policy FILE"))
	}
	contract, err := readDataAPIContractFile(*schema)
	if err != nil {
		return printErr("Invalid current schema", err)
	}
	policy, err := coveragePolicy(*policyPath)
	if err != nil {
		return printErr("Invalid coverage policy", err)
	}
	verification, err := readCoverageEvidence(*verificationPath, "data-api verify", contract.Fingerprint)
	if err != nil {
		return printErr("Invalid verification evidence", err)
	}
	plans, err := readCoverageEvidence(*plansPath, "data-api explain", contract.Fingerprint)
	if err != nil {
		return printErr("Invalid performance evidence", err)
	}
	var previous *dataAPIContract
	if *baseline != "" {
		value, err := readDataAPIContractFile(*baseline)
		if err != nil {
			return printErr("Invalid baseline schema", err)
		}
		previous = &value
	}
	report, err := evaluateDataAPICoverage(contract, policy, verification, plans, previous)
	if err != nil {
		return printErr("Invalid coverage mapping", err)
	}
	if jsonOutput {
		raw, _ := json.Marshal(report)
		if _, err := fmt.Fprintln(osStdout, string(raw)); err != nil {
			return printErr("Output failed", err)
		}
	} else {
		if _, err := fmt.Fprintf(osStdout, "Verification coverage: %s (%d missing requirements)\n", report.Status, report.Gaps); err != nil {
			return printErr("Output failed", err)
		}
		for _, relation := range report.Relations {
			if _, err := fmt.Fprintf(osStdout, "%s: verified %v; missing %v", relation.Relation, relation.Verified, relation.Missing); err != nil {
				return printErr("Output failed", err)
			}
			if relation.New {
				if _, err := fmt.Fprint(osStdout, "; new relation"); err != nil {
					return printErr("Output failed", err)
				}
			}
			if _, err := fmt.Fprintln(osStdout); err != nil {
				return printErr("Output failed", err)
			}
		}
	}
	if *check && report.Gaps > 0 {
		return 1
	}
	return 0
}
