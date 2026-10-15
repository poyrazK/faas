package main

import (
	"fmt"
	"math"
	"strings"
)

type testCampaignPolicySelector struct {
	Configuration *string `yaml:"configuration_sha256,omitempty"`
	Scenario      *string `yaml:"scenario,omitempty"`
	Profile       *string `yaml:"profile,omitempty"`
	Engine        *string `yaml:"engine,omitempty"`
	Case          *string `yaml:"case,omitempty"`
	Step          *string `yaml:"step,omitempty"`
	Phase         *string `yaml:"phase,omitempty"`
	Kind          *string `yaml:"kind,omitempty"`
	Assertion     *string `yaml:"assertion,omitempty"`
	Request       *string `yaml:"request,omitempty"`
}

type testCampaignScopedRule struct {
	Name                  string                     `yaml:"name"`
	Match                 testCampaignPolicySelector `yaml:"match"`
	FailNewFailures       *bool                      `yaml:"fail_new_failures,omitempty"`
	MaxFrequencyIncrease  *float64                   `yaml:"max_frequency_increase_percentage_points,omitempty"`
	DisableFrequencyCheck bool                       `yaml:"disable_frequency_check,omitempty"`
	MinExecutedRuns       *int                       `yaml:"min_executed_runs,omitempty"`
	NotComparable         *string                    `yaml:"on_not_comparable,omitempty"`
	InsufficientSamples   *string                    `yaml:"on_insufficient_samples,omitempty"`
}

type testCampaignPolicySettings struct {
	FailNewFailures      bool     `json:"fail_new_failures"`
	MaxFrequencyIncrease *float64 `json:"max_frequency_increase_percentage_points"`
	MinExecutedRuns      int      `json:"min_executed_runs"`
	NotComparable        string   `json:"on_not_comparable"`
	InsufficientSamples  string   `json:"on_insufficient_samples"`
}

type testCampaignPolicyDecision struct {
	ComparisonIndex int                        `json:"comparison_index"`
	PolicyRule      string                     `json:"policy_rule"`
	EffectivePolicy testCampaignPolicySettings `json:"effective_policy"`
}

func testCampaignSettings(p testCampaignPolicy) testCampaignPolicySettings {
	return testCampaignPolicySettings{p.FailNewFailures, p.MaxFrequencyIncrease, p.MinExecutedRuns, p.NotComparable, p.InsufficientSamples}
}

func validateTestCampaignScopedRules(rules []testCampaignScopedRule) error {
	if len(rules) > 100 {
		return fmt.Errorf("at most 100 scoped rules are allowed")
	}
	seen := make(map[string]bool)
	for _, r := range rules {
		if strings.TrimSpace(r.Name) == "" || r.Name == "default" || seen[r.Name] {
			return fmt.Errorf("scoped rule names must be unique, nonempty and not default")
		}
		seen[r.Name] = true
		m := r.Match
		if m.Configuration == nil && m.Scenario == nil && m.Profile == nil && m.Engine == nil && m.Case == nil &&
			m.Step == nil && m.Phase == nil && m.Kind == nil && m.Assertion == nil && m.Request == nil {
			return fmt.Errorf("rule %q requires at least one selector", r.Name)
		}
		if r.MinExecutedRuns != nil && *r.MinExecutedRuns < 1 {
			return fmt.Errorf("rule %q requires a positive sample minimum", r.Name)
		}
		if v := r.MaxFrequencyIncrease; v != nil && (r.DisableFrequencyCheck || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100) {
			return fmt.Errorf("rule %q has an invalid or disabled frequency limit", r.Name)
		}
		for _, action := range []*string{r.NotComparable, r.InsufficientSamples} {
			if action != nil && *action != "fail" && *action != "ignore" {
				return fmt.Errorf("rule %q handling actions must be fail or ignore", r.Name)
			}
		}
		if r.FailNewFailures == nil && r.MaxFrequencyIncrease == nil && !r.DisableFrequencyCheck &&
			r.MinExecutedRuns == nil && r.NotComparable == nil && r.InsufficientSamples == nil {
			return fmt.Errorf("rule %q must override at least one setting", r.Name)
		}
	}
	return nil
}

// First matching rule wins, with omitted settings inherited from global defaults.
// Context coverage cannot match assertion-level selectors whose evidence is absent.
func selectTestCampaignPolicy(policy testCampaignPolicy, key testFailureGroupKey, hash string, contextOnly bool) (testCampaignPolicy, string) {
	for _, rule := range policy.Rules {
		m := rule.Match
		if contextOnly && (m.Step != nil || m.Phase != nil || m.Kind != nil || m.Assertion != nil || m.Request != nil) {
			continue
		}
		pairs := []struct {
			want *string
			got  string
		}{
			{m.Configuration, hash}, {m.Scenario, key.Scenario}, {m.Profile, key.Profile}, {m.Engine, key.Engine},
			{m.Case, key.Case}, {m.Step, key.Step}, {m.Phase, key.Phase}, {m.Kind, key.Kind},
			{m.Assertion, key.Assertion}, {m.Request, key.Request},
		}
		matched := true
		for _, pair := range pairs {
			if pair.want != nil && *pair.want != pair.got {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		if rule.FailNewFailures != nil {
			policy.FailNewFailures = *rule.FailNewFailures
		}
		if rule.DisableFrequencyCheck {
			policy.MaxFrequencyIncrease = nil
		} else if rule.MaxFrequencyIncrease != nil {
			policy.MaxFrequencyIncrease = rule.MaxFrequencyIncrease
		}
		if rule.MinExecutedRuns != nil {
			policy.MinExecutedRuns = *rule.MinExecutedRuns
		}
		if rule.NotComparable != nil {
			policy.NotComparable = *rule.NotComparable
		}
		if rule.InsufficientSamples != nil {
			policy.InsufficientSamples = *rule.InsufficientSamples
		}
		return policy, rule.Name
	}
	return policy, "default"
}
