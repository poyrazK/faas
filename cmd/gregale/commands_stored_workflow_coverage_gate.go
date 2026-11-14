package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

type storedWorkflowCoverageExclusion struct {
	Step   string `yaml:"step" json:"step"`
	Code   string `yaml:"code" json:"code"`
	Loop   string `yaml:"loop,omitempty" json:"loop,omitempty"`
	Reason string `yaml:"reason" json:"reason"`
}

type storedWorkflowCoverageGate struct {
	Required   bool                              `json:"required"`
	Passed     bool                              `json:"passed"`
	Remaining  int                               `json:"remaining"`
	Exclusions []storedWorkflowCoverageExclusion `json:"exclusions"`
}

type storedWorkflowCoverageKey struct{ step, code, loop string }

func validateStoredWorkflowCoverageExclusions(definition api.WorkflowSpec, exclusions []storedWorkflowCoverageExclusion) error {
	if len(exclusions) > 256 {
		return errors.New("at most 256 coverage exclusions are allowed")
	}
	available := map[storedWorkflowCoverageKey]bool{}
	for _, hint := range (storedWorkflowScenarioCoverage{}).hints(definition) {
		available[storedWorkflowCoverageKey{hint.Step, hint.Code, hint.Loop}] = true
	}
	seen := map[storedWorkflowCoverageKey]bool{}
	for _, exclusion := range exclusions {
		key := storedWorkflowCoverageKey{exclusion.Step, exclusion.Code, exclusion.Loop}
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

func unexcludedStoredWorkflowCoverageHints(report storedWorkflowCheckReport) []storedWorkflowCoverageHint {
	excluded := map[storedWorkflowCoverageKey]bool{}
	if report.Coverage != nil {
		for _, entry := range report.Coverage.Exclusions {
			excluded[storedWorkflowCoverageKey{entry.Step, entry.Code, entry.Loop}] = true
		}
	}
	remaining := []storedWorkflowCoverageHint{}
	for _, hint := range report.CoverageHints {
		if !excluded[storedWorkflowCoverageKey{hint.Step, hint.Code, hint.Loop}] {
			remaining = append(remaining, hint)
		}
	}
	return remaining
}

func applyStoredWorkflowCoverageGate(report *storedWorkflowCheckReport, required bool, exclusions []storedWorkflowCoverageExclusion) {
	if !required && len(exclusions) == 0 {
		return
	}
	report.Coverage = &storedWorkflowCoverageGate{Required: required, Exclusions: append([]storedWorkflowCoverageExclusion{}, exclusions...)}
	report.Coverage.Remaining = len(unexcludedStoredWorkflowCoverageHints(*report))
	report.Coverage.Passed = report.Coverage.Remaining == 0
	if required && !report.Coverage.Passed {
		report.Passed = false
	}
}
