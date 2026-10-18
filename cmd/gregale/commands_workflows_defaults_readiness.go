package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type recordedRecoveryReadiness struct {
	Status               string            `json:"status"`
	RecordsPath          string            `json:"records_path"`
	Connection           previewConnection `json:"connection"`
	MatchingRuns         *int              `json:"matching_runs,omitempty"`
	MissingTimestamps    int               `json:"missing_timestamps,omitempty"`
	LatestReady          bool              `json:"latest_ready"`
	LatestRunID          string            `json:"latest_run_id,omitempty"`
	LatestRecordedAt     string            `json:"latest_recorded_at,omitempty"`
	Message              string            `json:"message"`
	InspectCommand       string            `json:"inspect_command"`
	RunSelectionTemplate string            `json:"run_selection_template,omitempty"`
}

func inspectWorkflowRecoveryReadiness(defaults localWorkflowDefaults, preferencePath string) recordedRecoveryReadiness {
	return inspectWorkflowRecoveryReadinessWithSelectors(defaults, preferencePath, []string{"--saved-workflow"})
}

func inspectWorkflowRecoveryReadinessWithSelectors(defaults localWorkflowDefaults, preferencePath string, selectors []string) recordedRecoveryReadiness {
	prefix := append([]string{"workflows", "resume-wait"}, selectors...)
	return inspectRecordedRecoveryReadiness(preferencePath, prefix, "No local records match the selected workflow/app on this connection; inspect recorded runs and the selectors. Remote history was not checked.", func(record acceptedRunRecord) bool {
		return record.Kind == "workflow" && record.Name == defaults.Workflow && record.App == defaults.App
	})
}

func inspectRecordedRecoveryReadiness(preferencePath string, prefix []string, noMatchesMessage string, matches func(acceptedRunRecord) bool) recordedRecoveryReadiness {
	report := recordedRecoveryReadiness{
		Status: "unreadable", RecordsPath: filepath.Join(filepath.Dir(preferencePath), "accepted-runs"), Connection: configuredPreviewConnection(),
		Message:        "Local records could not be safely read or validated; counts and latest selection are unknown. Inspect the project configuration report.",
		InspectCommand: savedNameSuggestionCommand([]string{"context", "--local-config", "--problems-only"}),
	}
	profile, endpoint := currentProfile(), apiBase()
	records, err := readRecordedReadinessRecords(report.RecordsPath)
	if err != nil {
		return report
	}
	matching := []acceptedRunRecord{}
	for _, record := range records {
		if record.Profile == profile && record.API == endpoint && matches(record) {
			matching = append(matching, record)
			if record.RecordedAt == "" {
				report.MissingTimestamps++
			}
		}
	}
	count := len(matching)
	report.MatchingRuns = &count
	if count == 0 {
		report.Status = "no_matches"
		report.Message = noMatchesMessage
		report.InspectCommand = savedNameSuggestionCommand(append(append([]string{}, prefix[:2]...), "--list", "--current-connection"))
		return report
	}
	report.InspectCommand = savedNameSuggestionCommand(append(append([]string{}, prefix...), "--list"))
	if report.MissingTimestamps > 0 {
		report.Status = "missing_timestamps"
		report.Message = "Matching older records lack recording times; choose an exact UUID from the local list instead of --latest."
	} else if latest, err := latestRecordedRun(matching); err != nil {
		report.Status = "tied_timestamps"
		report.Message = "The newest recording time is shared by several matching records; choose an exact UUID from the local list instead of --latest."
	} else {
		report.Status, report.LatestReady = "ready", true
		report.LatestRunID, report.LatestRecordedAt = latest.RunID, latest.RecordedAt
		report.Message = "The latest selector resolves locally; remote identity, status, and access were not checked."
		return report
	}
	report.RunSelectionTemplate = savedNameSuggestionCommand(append(append([]string{}, prefix...), "--run", "UUID", "--dry-run"))
	return report
}

func renderRecordedRecoveryReadiness(readiness recordedRecoveryReadiness, heading string) {
	_, _ = fmt.Fprintf(osStdout, "%s: %s\nRecords: %s\n", heading, readiness.Status, oneLine(readiness.RecordsPath))
	renderPreviewConnection(readiness.Connection)
	if readiness.MatchingRuns != nil {
		_, _ = fmt.Fprintf(osStdout, "Matching run records: %d\n", *readiness.MatchingRuns)
	}
	if readiness.MissingTimestamps > 0 {
		_, _ = fmt.Fprintf(osStdout, "Matching records without recording times: %d\n", readiness.MissingTimestamps)
	}
	if readiness.LatestReady {
		_, _ = fmt.Fprintf(osStdout, "Latest recorded run: %s\nRecorded at: %s\n", readiness.LatestRunID, readiness.LatestRecordedAt)
	}
	_, _ = fmt.Fprintf(osStdout, "%s\nInspect (POSIX shells):\n  %s\n", readiness.Message, readiness.InspectCommand)
	if readiness.RunSelectionTemplate != "" {
		_, _ = fmt.Fprintf(osStdout, "Preview an exact run (template; replace UUID with one from the list):\n  %s\n", readiness.RunSelectionTemplate)
	}
}

// Use the inventory's pinned, bounded reader and shared identity validation.
// A failed or incomplete scan never produces counts or a selected UUID.
func readRecordedReadinessRecords(path string) ([]acceptedRunRecord, error) {
	directory, err := inspectArtifactOutputDirectory(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	if directory.root == nil {
		return []acceptedRunRecord{}, nil
	}
	folder, err := directory.root.OpenFile(".", os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = folder.Close() }()
	records := []acceptedRunRecord{}
	entriesSeen, scanned := 0, 0
	for {
		entries, readErr := folder.ReadDir(128)
		for _, entry := range entries {
			entriesSeen++
			if entriesSeen > 10000 {
				return nil, errors.New("record directory scan exceeded its entry limit")
			}
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			scanned++
			if scanned > 1000 {
				return nil, errors.New("record scan exceeded its JSON limit")
			}
			record, reason := inspectLocalRecoveryRecord(directory.root, entry.Name())
			if reason != "" {
				return nil, errors.New(reason)
			}
			records = append(records, record)
		}
		if errors.Is(readErr, io.EOF) {
			return records, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}
