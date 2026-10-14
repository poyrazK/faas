package main

import (
	"errors"
	"flag"
	"fmt"
)

func cmdLogViewRename(args []string) int {
	return cmdLogViewTransfer(args, false)
}

func cmdLogViewCopy(args []string) int {
	return cmdLogViewTransfer(args, true)
}

func cmdLogViewTransfer(args []string, copying bool) int {
	operation, past := "rename", "Renamed"
	if copying {
		operation, past = "copy", "Copied"
	}
	fs := newFlagSet("logs views "+operation, flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview filters, destination, and preference impact without writing")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		return printErr("Invalid view arguments", fmt.Errorf("use logs views %s OLD_NAME NEW_NAME [--dry-run]", operation))
	}
	args = fs.Args()
	oldName, newName := args[0], args[1]
	for _, name := range args {
		if err := validateProfileName(name); err != nil {
			return printErr("Invalid log view name", err)
		}
	}
	if oldName == newName {
		return printErr("Names are identical", errors.New("choose a different destination name; no saved view was changed"))
	}
	views, err := loadLogViews()
	if err != nil {
		return printErr("Could not read log views", err)
	}
	view, exists := views.Views[oldName]
	if !exists {
		return unknownSavedName("Unknown log view", oldName, views.Views, func(candidate string) []string {
			command := []string{"logs", "views", operation, candidate, newName}
			if *dryRun {
				command = append(command, "--dry-run")
			}
			return command
		}, []string{"logs", "views", "list"})
	}
	if _, exists := views.Views[newName]; exists {
		return printErr("Destination view exists", errors.New("choose an unused destination name; existing views are never overwritten"))
	}
	path, err := logViewsPath()
	if err != nil {
		return printErr("Could not locate log views", err)
	}
	current, err := loadLogViews()
	if err != nil {
		return printErr("Could not re-read log views", err)
	}
	if !sameLogViews(current, views) {
		return printErr("Saved views changed", fmt.Errorf("review logs views list and repeat the intended %s", operation))
	}
	if *dryRun {
		return previewLogViewTransfer(operation, oldName, newName, view, path, copying)
	}
	var preference *logViewPreferenceGuidance
	if !copying {
		preference = inspectRemovedLogViewPreference(oldName, newName)
	}
	if !copying {
		delete(views.Views, oldName)
	}
	views.Views[newName] = view
	if err := writeLogViews(views); err != nil {
		return printErr("Could not "+operation+" saved view", err)
	}
	command := "gregale --profile " + recoveryArgument(currentProfile()) + " logs --view " + recoveryArgument(newName)
	receipt := struct {
		Operation       string                     `json:"operation"`
		SourcePreserved bool                       `json:"source_preserved"`
		From            string                     `json:"from"`
		To              string                     `json:"to"`
		View            logView                    `json:"view"`
		Path            string                     `json:"path"`
		RunCommand      string                     `json:"run_command"`
		Preference      *logViewPreferenceGuidance `json:"preference_guidance,omitempty"`
	}{operation, copying, oldName, newName, view, path, command, preference}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "%s saved view %s -> %s\nFilters preserved: %s\nPath: %s\nRun (POSIX shells; linked app, or add --app APP_SLUG):\n  %s\n", past, oldName, newName, formatLogViewFilters(view), oneLine(path), command)
	renderLogViewPreferenceGuidance(preference)
	return 0
}
