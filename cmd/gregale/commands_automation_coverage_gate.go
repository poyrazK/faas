package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationCoverageExclusion struct {
	Step   string `yaml:"step" json:"step"`
	Code   string `yaml:"code" json:"code"`
	Loop   string `yaml:"loop,omitempty" json:"loop,omitempty"`
	Reason string `yaml:"reason" json:"reason"`
}

type automationCoverageGate struct {
	Required   bool                          `json:"required"`
	Passed     bool                          `json:"passed"`
	Remaining  int                           `json:"remaining"`
	Exclusions []automationCoverageExclusion `json:"exclusions"`
}

type automationCoverageKey struct{ step, code, loop string }

func validateAutomationCoverageExclusions(definition api.WorkflowSpec, exclusions []automationCoverageExclusion) error {
	if len(exclusions) > 256 {
		return errors.New("at most 256 coverage exclusions are allowed")
	}
	available := map[automationCoverageKey]bool{}
	for _, hint := range (automationScenarioCoverage{}).hints(definition) {
		available[automationCoverageKey{hint.Step, hint.Code, hint.Loop}] = true
	}
	seen := map[automationCoverageKey]bool{}
	for _, exclusion := range exclusions {
		key := automationCoverageKey{exclusion.Step, exclusion.Code, exclusion.Loop}
		if !available[key] {
			return fmt.Errorf("coverage exclusion for step %q [%s] does not match a configured coverage path", exclusion.Step, exclusion.Code)
		}
		if seen[key] {
			return errors.New("duplicate coverage exclusion")
		}
		seen[key] = true
		if strings.TrimSpace(exclusion.Reason) == "" || len(exclusion.Reason) > 512 || strings.IndexFunc(exclusion.Reason, unicode.IsControl) >= 0 {
			return errors.New("coverage exclusions require a nonblank, single-line reason of at most 512 bytes")
		}
	}
	return nil
}

func unexcludedAutomationCoverageHints(report automationCheckReport) []automationCoverageHint {
	excluded := map[automationCoverageKey]bool{}
	if report.Coverage != nil {
		for _, entry := range report.Coverage.Exclusions {
			excluded[automationCoverageKey{entry.Step, entry.Code, entry.Loop}] = true
		}
	}
	remaining := []automationCoverageHint{}
	for _, hint := range report.CoverageHints {
		if !excluded[automationCoverageKey{hint.Step, hint.Code, hint.Loop}] {
			remaining = append(remaining, hint)
		}
	}
	return remaining
}

func applyAutomationCoverageGate(report *automationCheckReport, required bool, exclusions []automationCoverageExclusion) {
	if !required && len(exclusions) == 0 {
		return
	}
	report.Coverage = &automationCoverageGate{Required: required, Exclusions: append([]automationCoverageExclusion{}, exclusions...)}
	report.Coverage.Remaining = len(unexcludedAutomationCoverageHints(*report))
	report.Coverage.Passed = report.Coverage.Remaining == 0
	if required && !report.Coverage.Passed {
		report.Passed = false
	}
}
