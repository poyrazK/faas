package main

import (
	"errors"
	"fmt"
	"os"
)

func previewLogViewTransfer(operation, from, to string, view logView, path string, copying bool) int {
	impact, repair := "unchanged", ""
	if !copying {
		defaults, _, err := readLogDefaults()
		switch {
		case err != nil && !errors.Is(err, os.ErrNotExist):
			impact = "unknown"
		case err == nil && defaults.View == from:
			impact = "requires_update"
			repair = savedNameSuggestionCommand([]string{"logs", "defaults", "set", to})
		}
	}
	run := savedNameSuggestionCommand(append([]string{"logs"}, view.args()...))
	receipt := struct {
		Operation        string  `json:"operation"`
		DryRun           bool    `json:"dry_run"`
		Changed          bool    `json:"changed"`
		WouldChange      bool    `json:"would_change"`
		SourcePreserved  bool    `json:"source_preserved"`
		From             string  `json:"from"`
		To               string  `json:"to"`
		View             logView `json:"view"`
		Path             string  `json:"path"`
		PreferenceImpact string  `json:"preference_impact"`
		AfterCommand     string  `json:"after_rename_preference_command,omitempty"`
		RunCommand       string  `json:"run_command"`
		PreviewCommand   string  `json:"preview_command"`
	}{operation, true, false, true, copying, from, to, view, path, impact, repair, run, run + " --dry-run"}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "Preview %s saved view %s -> %s\nSource preserved after operation: %t\nFilters: %s\nPath: %s\n", operation, from, to, copying, formatLogViewFilters(view), oneLine(path))
	switch impact {
	case "requires_update":
		_, _ = fmt.Fprintf(osStdout, "The preferred view would still point to %s. After renaming, update it (POSIX shells):\n  %s\n", from, repair)
	case "unknown":
		_, _ = fmt.Fprintln(osStdout, "Preference impact: unknown; local preferences could not be safely read. Inspect with logs defaults before renaming.")
	default:
		_, _ = fmt.Fprintln(osStdout, "This operation does not remove a view referenced by the preferred-view setting.")
	}
	_, _ = fmt.Fprintf(osStdout, "Preview these filters (POSIX shells; linked app, or add --app APP_SLUG):\n  %s\nNo files were written or logs queried.\n", receipt.PreviewCommand)
	return 0
}
