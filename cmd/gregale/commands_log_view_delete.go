package main

import (
	"errors"
	"flag"
	"fmt"
)

func cmdLogViewDelete(args []string) int {
	fs := newFlagSet("logs views delete", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview deleting a saved view without writing")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		return printErr("Invalid deletion arguments", errors.New("use logs views delete NAME [--dry-run]"))
	}
	name := fs.Arg(0)
	if err := validateProfileName(name); err != nil {
		return printErr("Invalid log view name", err)
	}
	views, err := loadLogViews()
	if err != nil {
		return printErr("Could not read log views", err)
	}
	view, exists := views.Views[name]
	if !exists {
		return unknownSavedName("Unknown log view", name, views.Views, func(candidate string) []string {
			return correctedSavedNameFlags([]string{"logs", "views", "delete"}, fs, candidate, "")
		}, []string{"logs", "views", "list"})
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
		return printErr("Saved views changed", errors.New("review logs views list and retry the deletion"))
	}
	if *dryRun {
		preference := inspectRemovedLogViewPreference(name, "")
		if preference != nil {
			if preference.Status == "requires_update" {
				preference.Message = fmt.Sprintf("Deletion would leave the preferred-view setting pointing to %s. After deletion, clear it or select another saved view with logs defaults set NAME.", name)
			} else {
				preference.Message = "Preference impact is unknown because local preferences could not be safely read. Inspect the preferred view before deleting."
			}
		}
		if jsonOutput {
			return jsonOut(writeJSON(struct {
				Name              string                     `json:"name"`
				View              logView                    `json:"view"`
				Path              string                     `json:"path"`
				DryRun            bool                       `json:"dry_run"`
				Changed           bool                       `json:"changed"`
				WouldChange       bool                       `json:"would_change"`
				OtherViewsChanged bool                       `json:"other_views_changed"`
				Preference        *logViewPreferenceGuidance `json:"preference_guidance,omitempty"`
			}{name, view, path, true, false, true, false, preference}))
		}
		_, _ = fmt.Fprintf(osStdout, "Delete saved log view preview; no files changed.\nWould remove: %s\nSource: %s\nFilters: %s\nPath: %s\nOther saved views would remain unchanged.\n", name, view.Source, formatLogViewFilters(view), oneLine(path))
		renderLogViewPreferenceGuidance(preference)
		return 0
	}
	preference := inspectRemovedLogViewPreference(name, "")
	delete(views.Views, name)
	if err := writeLogViews(views); err != nil {
		return printErr("Could not delete log view", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Deleted    string                     `json:"deleted"`
			Preference *logViewPreferenceGuidance `json:"preference_guidance,omitempty"`
		}{name, preference}))
	}
	PrintOK(osStdout, "Deleted log view %s", name)
	renderLogViewPreferenceGuidance(preference)
	return 0
}
