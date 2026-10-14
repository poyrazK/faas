package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

type logViewUpdateReceipt struct {
	Name        string  `json:"name"`
	Before      logView `json:"before"`
	After       logView `json:"after"`
	Changed     bool    `json:"changed"`
	DryRun      bool    `json:"dry_run,omitempty"`
	WouldChange bool    `json:"would_change"`
	RunCommand  string  `json:"run_command"`
	Path        string  `json:"path"`
}

func cmdLogViewUpdate(args []string) int {
	if len(args) == 0 {
		return printErr("Missing log view", errors.New("use logs views update NAME with a filter or explicit clear option"))
	}
	name := args[0]
	if err := validateProfileName(name); err != nil {
		return printErr("Invalid log view name", err)
	}
	views, err := loadLogViews()
	if err != nil {
		return printErr("Could not read log views", err)
	}
	before, exists := views.Views[name]
	if !exists {
		return unknownSavedName("Unknown log view", name, views.Views, func(candidate string) []string {
			return append([]string{"logs", "views", "update", candidate}, args[1:]...)
		}, []string{"logs", "views", "list"})
	}
	after := before
	fs := newFlagSet("logs views update", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "validate proposed filters without saving or querying logs")
	fs.StringVar(&after.Since, "since", after.Since, "replace relative lookback")
	fs.StringVar(&after.Route, "route", after.Route, "replace HTTP route filter")
	fs.StringVar(&after.Grep, "grep", after.Grep, "replace runtime text filter")
	fs.StringVar(&after.Level, "level", after.Level, "replace runtime level filter")
	fs.IntVar(&after.Status, "status", after.Status, "replace exact HTTP status; 0 removes it")
	fs.IntVar(&after.Limit, "limit", after.Limit, "replace normal HTTP page size")
	fs.BoolVar(&after.Follow, "follow", after.Follow, "replace runtime follow mode; --follow=false disables it")
	clearRoute := fs.Bool("clear-route", false, "remove the route filter")
	clearGrep := fs.Bool("clear-grep", false, "remove the grep filter")
	clearLevel := fs.Bool("clear-level", false, "remove the level filter")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	provided := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	modified := provided["since"] || provided["route"] || provided["grep"] || provided["level"] || provided["status"] || provided["limit"] || provided["follow"] || *clearRoute || *clearGrep || *clearLevel
	if !modified || *clearRoute && provided["route"] || *clearGrep && provided["grep"] || *clearLevel && provided["level"] {
		return printErr("Invalid log view update", errors.New("select a filter or clear option; setting and clearing the same filter cannot be combined"))
	}
	if before.LatestRequestError && (provided["status"] || provided["limit"] || provided["follow"] || provided["grep"] || provided["level"]) {
		return printErr("Invalid latest-error view update", errors.New("latest request error views support only the relative window and route filters"))
	}
	if *clearRoute {
		after.Route = ""
	}
	if *clearGrep {
		after.Grep = ""
	}
	if *clearLevel {
		after.Level = ""
	}
	if err := validateLogView(after); err != nil {
		return printErr("Invalid resulting log view", err)
	}
	path, err := logViewsPath()
	if err != nil {
		return printErr("Could not locate log views", err)
	}
	changed := after != before
	if changed {
		current, err := loadLogViews()
		if err != nil {
			return printErr("Could not re-read log views", err)
		}
		if !sameLogViews(current, views) {
			return printErr("Saved views changed", errors.New("review logs views show NAME and repeat the intended update"))
		}
		if !*dryRun {
			views.Views[name] = after
			if err := writeLogViews(views); err != nil {
				return printErr("Could not save log view update", err)
			}
		}
	}
	runCommand := "gregale --profile " + recoveryArgument(currentProfile()) + " logs " + formatLogViewFilters(after)
	receipt := logViewUpdateReceipt{Name: name, Before: before, After: after, Changed: changed && !*dryRun, DryRun: *dryRun, WouldChange: changed, RunCommand: runCommand, Path: path}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "View %s: changed=%t; source=%s; latest-request-error=%t\nPath: %s\nBefore filters: %s\nAfter filters: %s\n", name, receipt.Changed, after.Source, after.LatestRequestError, oneLine(path), formatLogViewFilters(before), formatLogViewFilters(after))
	if *dryRun {
		_, _ = fmt.Fprintf(osStdout, "Preview only: would change=%t; saved filters are unchanged.\n", receipt.WouldChange)
	}
	_, _ = fmt.Fprintf(osStdout, "Run these filters (POSIX shells; linked app, or add --app APP_SLUG):\n  %s\n", runCommand)
	_, _ = fmt.Fprintln(osStdout, "Omitted settings were preserved. No view was executed.")
	return 0
}

func formatLogViewFilters(view logView) string {
	args := view.args()
	for i, arg := range args {
		args[i] = quoteLogCommandArg(arg)
	}
	return strings.Join(args, " ")
}

func sameLogViews(a, b logViewsFile) bool {
	if a.Version != b.Version || len(a.Views) != len(b.Views) {
		return false
	}
	for name, view := range a.Views {
		other, ok := b.Views[name]
		if !ok || view != other {
			return false
		}
	}
	return true
}
