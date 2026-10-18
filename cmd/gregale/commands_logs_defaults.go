package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

type localLogDefaults struct {
	Version int    `json:"version"`
	View    string `json:"view"`
}

func logDefaultsPath() (string, error) {
	path, err := jobDefaultsPath()
	return filepath.Join(filepath.Dir(path), "logs.json"), err
}

func readLogDefaults() (localLogDefaults, string, error) {
	var defaults localLogDefaults
	path, err := logDefaultsPath()
	if err != nil {
		return defaults, path, err
	}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return defaults, path, err
	}
	defer parent.Close()
	if parent.root == nil {
		return defaults, path, os.ErrNotExist
	}
	data, err := readCustomerOperationSourceFile(parent.root, filepath.Base(path), 8192)
	if err != nil {
		return defaults, path, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&defaults); err != nil {
		return defaults, path, errors.New("log defaults must contain one valid JSON object")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return defaults, path, errors.New("log defaults must contain one JSON object")
	}
	if defaults.Version != 1 {
		return defaults, path, errors.New("log defaults require version 1")
	}
	if err := validateProfileName(defaults.View); err != nil {
		return defaults, path, errors.New("log defaults require a valid saved view name")
	}
	return defaults, path, nil
}

func cmdLogsDefaults(args []string) int {
	if len(args) == 0 {
		args = []string{"show"}
	}
	path, err := logDefaultsPath()
	if err != nil {
		return printErr("Could not resolve log defaults", err)
	}
	switch args[0] {
	case "set":
		fs := newFlagSet("logs defaults set", flag.ContinueOnError)
		dryRun := fs.Bool("dry-run", false, "preview the preference and view filters without saving")
		if err := parseInterspersed(fs, args[1:]); err != nil {
			return printErr("Invalid log defaults arguments", err)
		}
		if fs.NArg() != 1 {
			return printErr("Invalid log defaults arguments", errors.New("use logs defaults set NAME with one existing saved view name"))
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
				return correctedSavedNameFlags([]string{"logs", "defaults", "set"}, fs, candidate, "")
			}, []string{"logs", "views", "list"})
		}
		defaults := localLogDefaults{Version: 1, View: name}
		if *dryRun {
			return previewLogDefaults(defaults, path, view)
		}
		if err := writeLocalSecretConfig(path, defaults); err != nil {
			return printErr("Could not save log defaults", err)
		}
		return renderLogDefaults(defaults, path, true, nil, "")
	case "show":
		fs := newFlagSet("logs defaults show", flag.ContinueOnError)
		app := fs.String("app", "", "target the preview command at this unverified app")
		if err := fs.Parse(args[1:]); err != nil {
			return printErr("Invalid log defaults arguments", err)
		}
		if fs.NArg() != 0 {
			return printErr("Invalid log defaults arguments", errors.New("use logs defaults show [--app SLUG]"))
		}
		if logsFlagWasSet(fs, "app") && !api.ValidAppSlug(*app) {
			return printErr("Invalid app target", errors.New("--app requires a valid nonempty app slug; app existence and access are not checked by this command"))
		}
		defaults, path, err := readLogDefaults()
		if errors.Is(err, os.ErrNotExist) {
			return renderLogDefaults(localLogDefaults{}, path, false, nil, *app)
		}
		if err != nil {
			return printErr("Could not read log defaults", err)
		}
		reference := inspectLogDefaultReference(defaults.View)
		if code := renderLogDefaults(defaults, path, true, &reference, *app); code != 0 {
			return code
		}
		if reference.Status != "available" {
			return 1
		}
		return 0
	case "clear":
		fs := newFlagSet("logs defaults clear", flag.ContinueOnError)
		dryRun := fs.Bool("dry-run", false, "preview removal of the local preference without deleting files")
		if err := fs.Parse(args[1:]); err != nil {
			return printErr("Invalid log defaults arguments", err)
		}
		if fs.NArg() != 0 {
			return printErr("Invalid log defaults arguments", errors.New("clear accepts only --dry-run"))
		}
		if *dryRun {
			return previewClearLogDefaults(path)
		}
		parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
		if err != nil {
			return printErr("Could not inspect log defaults", err)
		}
		defer parent.Close()
		if parent.root != nil {
			info, err := parent.root.Lstat(filepath.Base(path))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return printErr("Could not inspect log defaults", err)
			}
			if err == nil {
				if !info.Mode().IsRegular() {
					return printErr("Invalid log defaults file", errors.New("clear requires a regular file, not a symlink or directory"))
				}
				if err := parent.root.Remove(filepath.Base(path)); err != nil {
					return printErr("Could not clear log defaults", err)
				}
			}
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"cleared": true, "path": path}))
		}
		_, _ = fmt.Fprintf(osStdout, "Cleared local log defaults: %s\nSaved views and app targeting were unchanged.\n", oneLine(path))
		return 0
	default:
		return printErr("Unknown log defaults command", errors.New("use logs defaults set NAME, show, or clear"))
	}
}

func previewLogDefaults(proposed localLogDefaults, path string, view logView) int {
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return printErr("Could not inspect log defaults directory", err)
	}
	defer parent.Close()
	var before *localLogDefaults
	if parent.root != nil {
		info, err := parent.root.Lstat(filepath.Base(path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not inspect log defaults", err)
		}
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > 8192 {
				return printErr("Invalid log defaults file", errors.New("preview requires a regular, nonsymlink file within 8 KiB"))
			}
			defaults, foundPath, err := readLogDefaults()
			if err != nil || foundPath != path {
				return printErr("Could not preview log defaults", errors.New("existing defaults could not be safely read or validated; review them before previewing"))
			}
			before = &defaults
		}
	}
	preview := savedLogViewListingEntry(proposed.View, view, "").PreviewCommand
	report := struct {
		DryRun         bool              `json:"dry_run"`
		Changed        bool              `json:"changed"`
		WouldChange    bool              `json:"would_change"`
		Path           string            `json:"path"`
		BeforeSaved    bool              `json:"before_saved"`
		Before         *localLogDefaults `json:"before"`
		After          localLogDefaults  `json:"after"`
		Filters        logView           `json:"filters"`
		PreviewCommand string            `json:"preview_command"`
	}{true, false, before == nil || *before != proposed, path, before != nil, before, proposed, view, preview}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Log defaults preview; no files changed.\nPath: %s\n", oneLine(path))
	if before != nil {
		_, _ = fmt.Fprintf(osStdout, "Before: %s\n", before.View)
	} else {
		_, _ = fmt.Fprintln(osStdout, "Before: no preferred saved view")
	}
	_, _ = fmt.Fprintf(osStdout, "After: %s\n", proposed.View)
	renderSavedLogViewEntry(proposed.View, view, "")
	if !report.WouldChange {
		_, _ = fmt.Fprintln(osStdout, "Saved preference already matches; no change is needed.")
	}
	_, _ = fmt.Fprintln(osStdout, "The preview command uses the selected view directly; add --app SLUG if no app is linked. Remote logs and app access were not checked.")
	return 0
}

type logDefaultReference struct {
	Status                     string `json:"status"`
	Path                       string `json:"path,omitempty"`
	Source                     string `json:"source,omitempty"`
	LatestRequestError         bool   `json:"latest_request_error,omitempty"`
	Message                    string `json:"message"`
	InspectCommand             string `json:"inspect_command"`
	ReconfigureCommandTemplate string `json:"reconfigure_command_template,omitempty"`
	view                       *logView
}

func inspectLogDefaultReference(name string) logDefaultReference {
	path, err := logViewsPath()
	reference := logDefaultReference{Status: "unreadable", Path: path, Message: "Saved views could not be safely read or validated; review the file before reconfiguring the preference.", InspectCommand: savedNameSuggestionCommand([]string{"logs", "views", "list"})}
	if err != nil {
		return reference
	}
	info, inspectErr := os.Lstat(path)
	if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) {
		return reference
	}
	if inspectErr == nil && (!info.Mode().IsRegular() || info.Size() > 1<<20) {
		return reference
	}
	views, err := loadLogViews()
	if err != nil {
		return reference
	}
	view, exists := views.Views[name]
	if !exists {
		reference.Status = "missing"
		reference.Message = "The preferred view is not in the local saved-view store; choose an existing view."
		reference.ReconfigureCommandTemplate = savedNameSuggestionCommand([]string{"logs", "defaults", "set", "NAME"})
		return reference
	}
	reference.Status, reference.Source, reference.LatestRequestError = "available", view.Source, view.LatestRequestError
	reference.view = &view
	reference.Message = "The preferred view exists in the local saved-view store; app access and remote logs were not checked."
	return reference
}

func renderLogDefaults(defaults localLogDefaults, path string, saved bool, reference *logDefaultReference, app string) int {
	command := ""
	var filters *logView
	if reference != nil && reference.Status == "available" {
		filters = reference.view
	}
	if saved && (reference == nil || reference.Status == "available") {
		args := []string{"logs", "--saved-view", "--dry-run"}
		if app != "" {
			args = append(args, "--app", app)
		}
		command = savedNameSuggestionCommand(args)
	}
	var appVerified *bool
	if app != "" {
		verified := false
		appVerified = &verified
	}
	report := struct {
		Saved          bool                 `json:"saved"`
		Path           string               `json:"path"`
		View           string               `json:"view,omitempty"`
		PreviewCommand string               `json:"preview_command,omitempty"`
		Reference      *logDefaultReference `json:"reference,omitempty"`
		Filters        *logView             `json:"filters,omitempty"`
		App            string               `json:"app,omitempty"`
		AppVerified    *bool                `json:"app_verified,omitempty"`
	}{saved, path, defaults.View, command, reference, filters, app, appVerified}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Local log defaults\nPath: %s\n", oneLine(path))
	if app != "" {
		_, _ = fmt.Fprintf(osStdout, "Preview target: %s (unverified); the linked app is unchanged.\n", app)
	}
	if saved {
		_, _ = fmt.Fprintf(osStdout, "Preferred saved view: %s\n", defaults.View)
		if reference != nil {
			_, _ = fmt.Fprintf(osStdout, "Local reference: %s\nSaved-view file: %s\n%s\n", reference.Status, oneLine(reference.Path), reference.Message)
			if reference.Source != "" {
				_, _ = fmt.Fprintf(osStdout, "View source: %s; latest-request-error: %t\n", reference.Source, reference.LatestRequestError)
			}
			if reference.Status != "available" {
				_, _ = fmt.Fprintf(osStdout, "Inspect (POSIX shells):\n  %s\n", reference.InspectCommand)
			}
			if reference.ReconfigureCommandTemplate != "" {
				_, _ = fmt.Fprintf(osStdout, "Choose another view (template; replace NAME with an existing view):\n  %s\n", reference.ReconfigureCommandTemplate)
			}
		}
		if filters != nil {
			_, _ = fmt.Fprintf(osStdout, "Current saved filters: %s\n", savedLogViewFilterSummary(*filters))
		}
		if command != "" {
			_, _ = fmt.Fprintf(osStdout, "Preview (POSIX shells):\n  %s\n", command)
		}
		_, _ = fmt.Fprintln(osStdout, "View filters are loaded when used; app targeting and connection settings are unchanged.")
	} else {
		_, _ = fmt.Fprintln(osStdout, "No preferred saved view. Configure one with logs defaults set NAME, or use logs --view NAME explicitly.")
	}
	return 0
}
