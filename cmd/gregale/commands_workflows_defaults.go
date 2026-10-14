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
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type localWorkflowDefaults struct {
	Version  int    `json:"version"`
	Workflow string `json:"workflow"`
	App      string `json:"app"`
}

func workflowDefaultsPath() (string, error) {
	path, err := jobDefaultsPath()
	return filepath.Join(filepath.Dir(path), "workflows.json"), err
}

func validateWorkflowDefaults(defaults localWorkflowDefaults) error {
	if defaults.Version != 1 || strings.TrimSpace(defaults.Workflow) == "" || len(defaults.Workflow) > api.WorkflowWebhookNameMaxBytes || !api.ValidAppSlug(defaults.App) {
		return errors.New("workflow defaults require version 1, a nonempty workflow name within its size limit, and a valid app slug")
	}
	return nil
}

func readWorkflowDefaults() (localWorkflowDefaults, string, error) {
	var defaults localWorkflowDefaults
	path, err := workflowDefaultsPath()
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
	if decoder.Decode(&defaults) != nil {
		return defaults, path, errors.New("workflow defaults must contain one valid JSON object")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return defaults, path, errors.New("workflow defaults must contain one JSON object")
	}
	return defaults, path, validateWorkflowDefaults(defaults)
}

func cmdWorkflowsDefaults(args []string) int {
	if len(args) == 0 {
		args = []string{"show"}
	}
	path, err := workflowDefaultsPath()
	if err != nil {
		return printErr("Could not resolve workflow defaults", err)
	}
	switch args[0] {
	case "set":
		fs := newFlagSet("workflows defaults set", flag.ContinueOnError)
		app := fs.String("app", "", "recorded app slug for workflow recovery")
		dryRun := fs.Bool("dry-run", false, "preview selectors, destination, and local readiness without saving")
		if err := parseInterspersed(fs, args[1:]); err != nil {
			return printErr("Invalid workflow defaults arguments", err)
		}
		if fs.NArg() != 1 {
			return printErr("Invalid workflow defaults arguments", errors.New("use workflows defaults set NAME --app SLUG"))
		}
		defaults := localWorkflowDefaults{Version: 1, Workflow: fs.Arg(0), App: *app}
		if err := validateWorkflowDefaults(defaults); err != nil {
			return printErr("Invalid workflow defaults", err)
		}
		if *dryRun {
			return previewWorkflowDefaults(defaults, path)
		}
		if err := writeLocalSecretConfig(path, defaults); err != nil {
			return printErr("Could not save workflow defaults", err)
		}
		return renderWorkflowDefaults(defaults, path, true, nil)
	case "update":
		fs := newFlagSet("workflows defaults update", flag.ContinueOnError)
		app := fs.String("app", "", "replace the saved recorded app slug")
		dryRun := fs.Bool("dry-run", false, "preview updated selectors and local readiness without saving")
		if err := parseInterspersed(fs, args[1:]); err != nil {
			return printErr("Invalid workflow defaults arguments", err)
		}
		appProvided := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "app" {
				appProvided = true
			}
		})
		if fs.NArg() > 1 || (fs.NArg() == 0 && !appProvided) {
			return printErr("Invalid workflow defaults arguments", errors.New("use workflows defaults update [NAME] [--app SLUG]; provide a name or --app"))
		}
		before, foundPath, err := readWorkflowDefaults()
		if err != nil {
			return printErr("Could not read workflow defaults", err)
		}
		if foundPath != path {
			return printErr("Defaults changed", errors.New("the defaults root changed; retry the update"))
		}
		defaults := before
		if fs.NArg() == 1 {
			defaults.Workflow = fs.Arg(0)
		}
		if appProvided {
			defaults.App = *app
		}
		if err := validateWorkflowDefaults(defaults); err != nil {
			return printErr("Invalid workflow defaults", err)
		}
		if *dryRun {
			return previewWorkflowDefaultsWithBefore(defaults, path, &before)
		}
		if err := writeLocalSecretConfig(path, defaults); err != nil {
			return printErr("Could not save workflow defaults", err)
		}
		return renderWorkflowDefaults(defaults, path, true, nil)
	case "show":
		if len(args) != 1 {
			return printErr("Invalid workflow defaults arguments", errors.New("show accepts no arguments"))
		}
		defaults, path, err := readWorkflowDefaults()
		if errors.Is(err, os.ErrNotExist) {
			return renderWorkflowDefaults(localWorkflowDefaults{}, path, false, nil)
		}
		if err != nil {
			return printErr("Could not read workflow defaults", err)
		}
		readiness := inspectWorkflowRecoveryReadiness(defaults, path)
		if code := renderWorkflowDefaults(defaults, path, true, &readiness); code != 0 {
			return code
		}
		if readiness.Status == "unreadable" {
			return 1
		}
		return 0
	case "clear":
		fs := newFlagSet("workflows defaults clear", flag.ContinueOnError)
		dryRun := fs.Bool("dry-run", false, "preview preference removal without deleting files")
		if err := fs.Parse(args[1:]); err != nil {
			return 1
		}
		if rejectUnexpectedFlagArgs(fs) {
			return 1
		}
		if *dryRun {
			return previewClearWorkflowDefaults(path)
		}
		parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
		if err != nil {
			return printErr("Could not inspect workflow defaults", err)
		}
		defer parent.Close()
		if parent.root != nil {
			info, err := parent.root.Lstat(filepath.Base(path))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return printErr("Could not inspect workflow defaults", err)
			}
			if err == nil {
				if !info.Mode().IsRegular() {
					return printErr("Invalid workflow defaults file", errors.New("clear requires a regular file, not a symlink or directory"))
				}
				if err := parent.root.Remove(filepath.Base(path)); err != nil {
					return printErr("Could not clear workflow defaults", err)
				}
			}
		}
		if jsonOutput {
			return jsonOut(writeJSONSingle(map[string]any{"cleared": true, "path": path}))
		}
		_, _ = fmt.Fprintf(osStdout, "Cleared local workflow defaults: %s\n", oneLine(path))
		return 0
	default:
		return printErr("Unknown workflow defaults command", errors.New("use workflows defaults set NAME --app SLUG, update [NAME] [--app SLUG], show, or clear"))
	}
}

func renderWorkflowDefaults(defaults localWorkflowDefaults, path string, saved bool, readiness *recordedRecoveryReadiness) int {
	command := ""
	if saved && (readiness == nil || readiness.LatestReady) {
		command = savedNameSuggestionCommand([]string{"workflows", "resume-wait", "--saved-workflow", "--latest", "--dry-run"})
	}
	report := struct {
		Saved          bool                       `json:"saved"`
		Path           string                     `json:"path"`
		Workflow       string                     `json:"workflow,omitempty"`
		App            string                     `json:"app,omitempty"`
		PreviewCommand string                     `json:"preview_command,omitempty"`
		Readiness      *recordedRecoveryReadiness `json:"readiness,omitempty"`
	}{saved, path, defaults.Workflow, defaults.App, command, readiness}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Local workflow recovery defaults\nPath: %s\n", oneLine(path))
	if saved {
		_, _ = fmt.Fprintf(osStdout, "Workflow: %s\nRecorded app selector: %s\n", oneLine(defaults.Workflow), defaults.App)
		if readiness != nil {
			renderRecordedRecoveryReadiness(*readiness, "Local recovery readiness")
		}
		if command != "" {
			_, _ = fmt.Fprintf(osStdout, "Preview (POSIX shells):\n  %s\n", command)
		}
	} else {
		_, _ = fmt.Fprintln(osStdout, "No saved workflow defaults. Configure workflows defaults set NAME --app SLUG, or supply explicit recovery selectors.")
	}
	if readiness == nil {
		_, _ = fmt.Fprintln(osStdout, "Targets and available local runs were not verified; linked targets and run settings are unchanged.")
	} else {
		_, _ = fmt.Fprintln(osStdout, "All checks were offline; no records, preferences, or linked targets were changed.")
	}
	return 0
}
