package main

import (
	"errors"
	"fmt"
)

func previewSaveLogView(name string, view logView, replace bool) int {
	if err := validateProfileName(name); err != nil {
		return printErr("Invalid log view name", err)
	}
	views, err := loadLogViews()
	if err != nil {
		return printErr("Could not read log views", err)
	}
	previous, exists := views.Views[name]
	if exists && !replace {
		return printErr("Could not save log view", errors.New("view already exists; choose a new name or use --replace"))
	}
	path, err := logViewsPath()
	if err != nil {
		return printErr("Could not locate log views", err)
	}
	var before *logView
	if exists {
		before = &previous
	}
	runCommand := savedNameSuggestionCommand(append([]string{"logs"}, view.args()...))
	receipt := struct {
		Name           string   `json:"name"`
		Path           string   `json:"path"`
		Before         *logView `json:"before"`
		After          logView  `json:"after"`
		DryRun         bool     `json:"dry_run"`
		Changed        bool     `json:"changed"`
		WouldChange    bool     `json:"would_change"`
		Replacing      bool     `json:"replacing"`
		RunCommand     string   `json:"run_command"`
		PreviewCommand string   `json:"preview_command"`
	}{name, path, before, view, true, false, !exists || previous != view, exists, runCommand, runCommand + " --dry-run"}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "Preview saving view %s: replacing=%t; would change=%t\nPath: %s\n", name, exists, receipt.WouldChange, oneLine(path))
	if before != nil {
		_, _ = fmt.Fprintf(osStdout, "Before filters: %s\n", formatLogViewFilters(*before))
	}
	_, _ = fmt.Fprintf(osStdout, "Proposed filters: %s\nRun (POSIX shells; linked app, or add --app APP_SLUG):\n  %s\nPreview:\n  %s\nNo files were written or logs queried.\n", formatLogViewFilters(view), receipt.RunCommand, receipt.PreviewCommand)
	return 0
}
