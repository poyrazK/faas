package main

import "slices"

const (
	listOutputRequiresAll      = "list_output_requires_all"
	listStreamRequiresAllJSON  = "list_stdout_stream_requires_all_and_json"
	listProgressRequiresExport = "list_progress_requires_file_export"
)

// Conditions use registered option names, never supplied values. "present"
// tests flag presence; "enabled" tests a parsed Boolean; "effective" tests
// JSON mode after global options, environment and preferences are applied.
type cliFlagRuleCondition struct {
	Flag  string `json:"flag"`
	Test  string `json:"test"`
	Value bool   `json:"value"`
}

type cliFlagRule struct {
	ID       string                 `json:"id"`
	Kind     string                 `json:"kind"`
	When     []cliFlagRuleCondition `json:"when"`
	Requires []cliFlagRuleCondition `json:"requires"`
	message  string
}

type listFlagRuleState struct{ All, Stream, Output, JSON, Progress bool }

type cliFlagRuleDiagnostic struct {
	Kind     string                 `json:"kind"`
	Rule     cliFlagRule            `json:"rule"`
	Missing  []cliFlagRuleCondition `json:"missing"`
	HelpArgv []string               `json:"help_argv"`
}

type listFlagRuleError struct {
	rule    cliFlagRule
	missing []cliFlagRuleCondition
	command string
}

func (e *listFlagRuleError) Error() string { return e.rule.message }

// Fresh owned rules supply discovery and the two existing validation points.
// Their order does not reorder validation of paths, limits or authentication.
func listFlagRules() []cliFlagRule {
	return []cliFlagRule{
		{ID: listOutputRequiresAll, Kind: "requires", message: "--output requires --all",
			When:     []cliFlagRuleCondition{{Flag: "output", Test: "present", Value: true}},
			Requires: []cliFlagRuleCondition{{Flag: "all", Test: "enabled", Value: true}}},
		{ID: listStreamRequiresAllJSON, Kind: "requires", message: "--stream requires --json and --all",
			When:     []cliFlagRuleCondition{{Flag: "stream", Test: "enabled", Value: true}, {Flag: "output", Test: "present", Value: false}},
			Requires: []cliFlagRuleCondition{{Flag: "all", Test: "enabled", Value: true}, {Flag: "json", Test: "effective", Value: true}}},
		{ID: listProgressRequiresExport, Kind: "requires", message: "--progress requires --all and --output",
			When:     []cliFlagRuleCondition{{Flag: "progress", Test: "active", Value: true}},
			Requires: []cliFlagRuleCondition{{Flag: "output", Test: "present", Value: true}, {Flag: "all", Test: "enabled", Value: true}}},
	}
}

func (state listFlagRuleState) matches(condition cliFlagRuleCondition) bool {
	var value bool
	switch {
	case condition.Flag == "output" && condition.Test == "present":
		value = state.Output
	case condition.Flag == "all" && condition.Test == "enabled":
		value = state.All
	case condition.Flag == "stream" && condition.Test == "enabled":
		value = state.Stream
	case condition.Flag == "json" && condition.Test == "effective":
		value = state.JSON
	case condition.Flag == "progress" && condition.Test == "active":
		value = state.Progress
	default:
		return false
	}
	return value == condition.Value
}

func listFlagRuleViolation(id string, state listFlagRuleState, command string) *listFlagRuleError {
	for _, rule := range listFlagRules() {
		if rule.ID != id {
			continue
		}
		for _, condition := range rule.When {
			if !state.matches(condition) {
				return nil
			}
		}
		missing := []cliFlagRuleCondition{}
		for _, condition := range rule.Requires {
			if !state.matches(condition) {
				missing = append(missing, condition)
			}
		}
		if len(missing) != 0 {
			return &listFlagRuleError{rule: rule, missing: missing, command: command}
		}
	}
	return nil
}

func visibleListFlagRules(available []string) []cliFlagRule {
	rules := []cliFlagRule{}
	for _, rule := range listFlagRules() {
		visible := true
		when := []cliFlagRuleCondition{}
		for _, condition := range rule.When {
			if !slices.Contains(available, condition.Flag) {
				if condition.Value {
					visible = false
					break
				}
				continue
			}
			when = append(when, condition)
		}
		for _, condition := range rule.Requires {
			if condition.Test != "effective" && !slices.Contains(available, condition.Flag) {
				visible = false
			}
		}
		if visible {
			rule.When = when
			rules = append(rules, rule)
		}
	}
	return rules
}
