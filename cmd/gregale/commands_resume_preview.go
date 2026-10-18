package main

import (
	"fmt"
	"path/filepath"
	"time"
)

func previewRecordedJobAction(record acceptedRunRecord, selection string, logs, results bool, timeout, interval time.Duration) int {
	action := "wait"
	args := []string{"jobs", "resume", "--job", record.Name, "--run", record.RunID}
	notes := []string{"The selected identity was validated locally; remote identity, status, and access were not checked.", "This preview does not submit, replay, wait for, or modify a run."}
	outputDirectory := ""
	if logs {
		action = "logs"
		args = append(args, "--logs")
	} else {
		if results {
			action = "results"
			args = append(args, "--results")
			var err error
			outputDirectory, err = filepath.Abs(filepath.Join("results", record.Name, record.RunID))
			if err != nil {
				return printErr("Could not resolve result destination", err)
			}
			notes = append(notes, "Result destination and artifact-filter rules were not validated; collection retains its existing behavior.")
		}
		args = append(args, "--timeout", timeout.String(), "--poll-interval", interval.String())
	}
	report := struct {
		DryRun           bool              `json:"dry_run"`
		Selection        string            `json:"selection"`
		Job              string            `json:"job"`
		RunID            string            `json:"run_id"`
		RecordedAt       string            `json:"recorded_at,omitempty"`
		Action           string            `json:"action"`
		Connection       previewConnection `json:"connection"`
		Timeout          string            `json:"timeout,omitempty"`
		PollInterval     string            `json:"poll_interval,omitempty"`
		ResultsDirectory string            `json:"results_directory,omitempty"`
		ActionCommand    string            `json:"action_command"`
		Notes            []string          `json:"notes"`
	}{DryRun: true, Selection: selection, Job: record.Name, RunID: record.RunID, RecordedAt: record.RecordedAt, Action: action, Connection: configuredPreviewConnection(), ResultsDirectory: outputDirectory, ActionCommand: savedNameSuggestionCommand(args), Notes: notes}
	if !logs {
		report.Timeout, report.PollInterval = timeout.String(), interval.String()
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Job recovery preview (offline); no files changed.\nJob: %s\nSelected run: %s\nSelection: %s\nAction: %s\n", oneLine(record.Name), record.RunID, selection, action)
	if record.RecordedAt != "" {
		_, _ = fmt.Fprintf(osStdout, "Recorded at: %s\n", record.RecordedAt)
	}
	renderPreviewConnection(report.Connection)
	if !logs {
		_, _ = fmt.Fprintf(osStdout, "Maximum wait: %s; poll interval: %s\n", report.Timeout, report.PollInterval)
	}
	if outputDirectory != "" {
		_, _ = fmt.Fprintf(osStdout, "Result destination (not validated): %s\n", oneLine(outputDirectory))
	}
	_, _ = fmt.Fprintf(osStdout, "Observe this exact run (POSIX shells):\n  %s\n", report.ActionCommand)
	for _, note := range notes {
		_, _ = fmt.Fprintln(osStdout, note)
	}
	return 0
}

func previewRecordedWorkflowAction(record acceptedRunRecord, selection string, diagnose bool, timeout, interval time.Duration, defaultsPath string) int {
	action := "wait"
	args := []string{"workflows", "resume-wait", "--workflow", record.Name, "--run", record.RunID}
	if record.App != "" {
		args = append(args, "--app", record.App)
	}
	if diagnose {
		action = "diagnose"
		args = append(args, "--diagnose")
	} else {
		args = append(args, "--timeout", timeout.String(), "--poll-interval", interval.String())
	}
	report := struct {
		DryRun        bool              `json:"dry_run"`
		Selection     string            `json:"selection"`
		Workflow      string            `json:"workflow"`
		DefaultsPath  string            `json:"defaults_path,omitempty"`
		App           string            `json:"app,omitempty"`
		AppID         string            `json:"app_id,omitempty"`
		RunID         string            `json:"run_id"`
		RecordedAt    string            `json:"recorded_at,omitempty"`
		Action        string            `json:"action"`
		Connection    previewConnection `json:"connection"`
		Timeout       string            `json:"timeout,omitempty"`
		PollInterval  string            `json:"poll_interval,omitempty"`
		ActionCommand string            `json:"action_command"`
		Notes         []string          `json:"notes"`
	}{DryRun: true, Selection: selection, Workflow: record.Name, DefaultsPath: defaultsPath, App: record.App, AppID: record.AppID, RunID: record.RunID, RecordedAt: record.RecordedAt, Action: action, Connection: configuredPreviewConnection(), ActionCommand: savedNameSuggestionCommand(args), Notes: []string{"The selected identity was validated locally; remote identity, status, diagnostics, and access were not checked.", "App details come from the saved record, not the linked app; missing fields were not inferred.", "This preview does not submit, replay, wait for, or modify a run."}}
	if !diagnose {
		report.Timeout, report.PollInterval = timeout.String(), interval.String()
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Workflow recovery preview (offline); no files changed.\nWorkflow: %s\nSelected run: %s\nSelection: %s\nAction: %s\n", oneLine(record.Name), record.RunID, selection, action)
	if defaultsPath != "" {
		_, _ = fmt.Fprintf(osStdout, "Workflow defaults: %s\n", oneLine(defaultsPath))
	}
	if record.App != "" {
		_, _ = fmt.Fprintf(osStdout, "Recorded app: %s\n", oneLine(record.App))
	}
	if record.AppID != "" {
		_, _ = fmt.Fprintf(osStdout, "Recorded app ID: %s\n", oneLine(record.AppID))
	}
	if record.RecordedAt != "" {
		_, _ = fmt.Fprintf(osStdout, "Recorded at: %s\n", record.RecordedAt)
	}
	renderPreviewConnection(report.Connection)
	if !diagnose {
		_, _ = fmt.Fprintf(osStdout, "Maximum wait: %s; poll interval: %s\n", report.Timeout, report.PollInterval)
	}
	_, _ = fmt.Fprintf(osStdout, "Observe this exact run (POSIX shells):\n  %s\n", report.ActionCommand)
	for _, note := range report.Notes {
		_, _ = fmt.Fprintln(osStdout, note)
	}
	return 0
}
