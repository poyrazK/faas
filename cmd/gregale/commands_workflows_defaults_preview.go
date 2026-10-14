package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func previewWorkflowDefaults(proposed localWorkflowDefaults, path string) int {
	return previewWorkflowDefaultsWithBefore(proposed, path, nil)
}

func previewWorkflowDefaultsWithBefore(proposed localWorkflowDefaults, path string, expectedBefore *localWorkflowDefaults) int {
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return printErr("Could not inspect workflow defaults directory", err)
	}
	defer parent.Close()
	var before *localWorkflowDefaults
	if parent.root != nil {
		info, err := parent.root.Lstat(filepath.Base(path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not inspect workflow defaults", err)
		}
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > 8192 {
				return printErr("Invalid workflow defaults file", errors.New("preview requires a regular, nonsymlink file within 8 KiB"))
			}
			defaults, foundPath, err := readWorkflowDefaults()
			if err != nil || foundPath != path {
				return printErr("Could not preview workflow defaults", errors.New("existing defaults could not be safely read or validated; review them before previewing"))
			}
			before = &defaults
		}
	}
	if expectedBefore != nil && (before == nil || *before != *expectedBefore) {
		return printErr("Defaults changed", errors.New("saved selectors changed; retry the update preview"))
	}
	selectors := []string{"--workflow", proposed.Workflow, "--app", proposed.App}
	readiness := inspectWorkflowRecoveryReadinessWithSelectors(proposed, path, selectors)
	command := ""
	if readiness.LatestReady {
		args := append([]string{"workflows", "resume-wait"}, selectors...)
		command = savedNameSuggestionCommand(append(args, "--latest", "--dry-run"))
	}
	report := struct {
		DryRun         bool                      `json:"dry_run"`
		Changed        bool                      `json:"changed"`
		WouldChange    bool                      `json:"would_change"`
		Path           string                    `json:"path"`
		BeforeSaved    bool                      `json:"before_saved"`
		Before         *localWorkflowDefaults    `json:"before"`
		After          localWorkflowDefaults     `json:"after"`
		Readiness      recordedRecoveryReadiness `json:"readiness"`
		PreviewCommand string                    `json:"preview_command,omitempty"`
	}{true, false, before == nil || *before != proposed, path, before != nil, before, proposed, readiness, command}
	if jsonOutput {
		if code := jsonOut(writeJSONSingle(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Workflow defaults preview; no files changed.\nPath: %s\n", oneLine(path))
		if before == nil {
			_, _ = fmt.Fprintln(osStdout, "Before: no saved workflow defaults")
		} else {
			_, _ = fmt.Fprintf(osStdout, "Before: %s (app=%s)\n", oneLine(before.Workflow), before.App)
		}
		_, _ = fmt.Fprintf(osStdout, "After: %s (app=%s)\n", oneLine(proposed.Workflow), proposed.App)
		if !report.WouldChange {
			_, _ = fmt.Fprintln(osStdout, "Saved selectors already match; no change is needed.")
		}
		renderRecordedRecoveryReadiness(readiness, "Proposed recovery readiness")
		if command != "" {
			_, _ = fmt.Fprintf(osStdout, "Preview proposed recovery (POSIX shells):\n  %s\n", command)
		}
		_, _ = fmt.Fprintln(osStdout, "Commands use explicit proposed selectors; preferences, records, and linked targets were unchanged. Destination write permissions and remote resources were not verified.")
	}
	if readiness.Status == "unreadable" {
		return 1
	}
	return 0
}
