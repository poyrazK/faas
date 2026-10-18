package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func previewClearWorkflowDefaults(path string) int {
	report := struct {
		Path        string                 `json:"path"`
		DryRun      bool                   `json:"dry_run"`
		Changed     bool                   `json:"changed"`
		Cleared     bool                   `json:"cleared"`
		WouldChange bool                   `json:"would_change"`
		BeforeSaved bool                   `json:"before_saved"`
		Before      *localWorkflowDefaults `json:"before"`
		After       *localWorkflowDefaults `json:"after"`
		Effects     []string               `json:"effects"`
		ListCommand string                 `json:"list_command,omitempty"`
	}{Path: path, DryRun: true, Effects: []string{}}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return printErr("Could not inspect workflow defaults directory", err)
	}
	defer parent.Close()
	if parent.root != nil {
		info, err := parent.root.Lstat(filepath.Base(path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not inspect workflow defaults", err)
		}
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > 8192 {
				return printErr("Invalid workflow defaults file", errors.New("preview requires a regular, nonsymlink file within 8 KiB"))
			}
			before, foundPath, err := readWorkflowDefaults()
			if err != nil || foundPath != path {
				return printErr("Could not preview clearing workflow defaults", errors.New("existing defaults could not be safely read or validated; inspect the file before retrying"))
			}
			report.Before, report.BeforeSaved, report.WouldChange = &before, true, true
			report.Effects = []string{"The project will have no saved workflow/app selectors; --saved-workflow will require new preferences.", "Recorded runs remain available with explicit selectors or run UUIDs; linked targets are unchanged."}
			report.ListCommand = savedNameSuggestionCommand([]string{"workflows", "resume-wait", "--workflow", before.Workflow, "--app", before.App, "--list"})
		}
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Clear workflow defaults preview; no files changed.\nPath: %s\n", oneLine(path))
	if !report.BeforeSaved {
		_, _ = fmt.Fprintln(osStdout, "No saved workflow defaults; nothing to clear.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Would remove workflow: %s\nRecorded app selector: %s\nAfter: no saved workflow defaults\n", oneLine(report.Before.Workflow), report.Before.App)
	for _, effect := range report.Effects {
		_, _ = fmt.Fprintln(osStdout, effect)
	}
	_, _ = fmt.Fprintf(osStdout, "List recorded runs (offline; POSIX shells):\n  %s\n", report.ListCommand)
	return 0
}
