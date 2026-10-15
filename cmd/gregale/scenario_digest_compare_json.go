package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type testDigestComparisonReport struct {
	SchemaVersion int                         `json:"schema_version"`
	Informational bool                        `json:"informational"`
	Counts        map[string]int              `json:"counts"`
	Comparisons   []testDigestComparisonRecord `json:"comparisons"`
}

type testDigestComparisonRecord struct {
	ConfigurationSHA256 string                         `json:"configuration_sha256,omitempty"`
	Key                 testFailureGroupKey            `json:"key"`
	Status              string                         `json:"status"`
	Reason              string                         `json:"not_comparable_reason,omitempty"`
	FrequencyDeltaPP    *float64                       `json:"frequency_delta_percentage_points"`
	Baseline            testDigestComparisonObservation `json:"baseline"`
	Current             testDigestComparisonObservation `json:"current"`
}

type testDigestComparisonObservation struct {
	Failures             int                     `json:"failures"`
	ExecutedRuns         int                     `json:"executed_runs"`
	WorstObserved        *float64                `json:"worst_observed"`
	WorstLimit           *float64                `json:"worst_limit"`
	Operator             string                  `json:"operator,omitempty"`
	FirstSupportingIndex *int                    `json:"first_supporting_receipt_index"`
	EvidenceURL          string                  `json:"evidence_url,omitempty"`
	Occurrences          []testFailureOccurrence `json:"occurrences"`
}

func marshalTestDigestComparison(before, after []testRunReceipt, baselineURL, currentURL string) ([]byte, error) {
	report := testDigestComparisonReport{
		SchemaVersion: 1, Informational: true,
		Counts: map[string]int{"newly_observed": 0, "persisting": 0, "no_longer_observed": 0, "not_comparable": 0},
		Comparisons: make([]testDigestComparisonRecord, 0),
	}
	for _, c := range compareTestDigestGroups(before, after) {
		report.Counts[c.status]++
		report.Comparisons = append(report.Comparisons, testDigestComparisonRecord{
			ConfigurationSHA256: c.fingerprint, Key: c.key, Status: c.status, Reason: c.reason, FrequencyDeltaPP: c.delta,
			Baseline: testDigestComparisonObservationFor(c.before, c.beforeRuns, baselineURL),
			Current: testDigestComparisonObservationFor(c.after, c.afterRuns, currentURL),
		})
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func testDigestComparisonObservationFor(row testDigestGroup, runs int, evidence string) testDigestComparisonObservation {
	observation := testDigestComparisonObservation{
		Failures: row.group.Failures, ExecutedRuns: runs,
		WorstObserved: row.group.WorstObserved, WorstLimit: row.group.WorstLimit,
		Operator: row.group.Operator, EvidenceURL: evidence,
		Occurrences: append([]testFailureOccurrence{}, row.group.Occurrences...),
	}
	if row.group.Failures > 0 {
		index := row.root
		observation.FirstSupportingIndex = &index
		if evidence != "" {
			observation.EvidenceURL = fmt.Sprintf("%s#run-evidence-%d", evidence, index)
		}
	}
	return observation
}

// Reject equal paths and existing filesystem aliases before writing either output.
func testDigestDistinctOutputs(markdown, comparison string) error {
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			return resolved, nil
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(absolute)); err == nil {
			return filepath.Join(parent, filepath.Base(absolute)), nil
		}
		return absolute, nil
	}
	a, err := canonical(markdown)
	if err != nil {
		return err
	}
	b, err := canonical(comparison)
	if err != nil {
		return err
	}
	left, leftErr := os.Stat(markdown)
	right, rightErr := os.Stat(comparison)
	if a == b || (leftErr == nil && rightErr == nil && os.SameFile(left, right)) {
		return fmt.Errorf("--markdown and --comparison-json must use distinct output files")
	}
	return nil
}
