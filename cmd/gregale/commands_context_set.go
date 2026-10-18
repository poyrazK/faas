package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type contextUpdateReceipt struct {
	Preset      string              `json:"preset,omitempty"`
	PresetPath  string              `json:"preset_path,omitempty"`
	Before      localProjectContext `json:"before"`
	After       localProjectContext `json:"after"`
	Path        string              `json:"path"`
	Changed     bool                `json:"changed"`
	DryRun      bool                `json:"dry_run,omitempty"`
	WouldChange bool                `json:"would_change"`
	Connection  *connectionContext  `json:"connection"`
}

func cmdContextSet(args []string) int {
	return cmdContextSetSelected(args, nil)
}

type contextUpdateSelection struct {
	Link       localProjectContext
	Path       string
	Preset     string
	PresetPath string
}

func cmdContextSetSelected(args []string, selection *contextUpdateSelection) int {
	fs := newFlagSet("context set", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "validate and show proposed defaults without saving")
	app := fs.String("app", "", "replace only the linked default app")
	environment := fs.String("environment", "", "replace only the linked default environment")
	clearApp := fs.Bool("clear-app", false, "remove the default app; preserve other settings")
	clearEnvironment := fs.Bool("clear-environment", false, "remove the default environment; preserve other settings")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	appSet, environmentSet := false, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "app" {
			appSet = true
		}
		if f.Name == "environment" {
			environmentSet = true
		}
	})
	if !appSet && !environmentSet && !*clearApp && !*clearEnvironment || appSet && *clearApp || environmentSet && *clearEnvironment {
		return printErr("Invalid context update", errors.New("select --app or --environment, or explicitly clear with --clear-app or --clear-environment; setting and clearing the same field cannot be combined"))
	}
	if appSet && !api.ValidAppSlug(*app) {
		return printErr("Invalid default app", errors.New("--app requires a valid nonempty app slug; use --clear-app to remove it"))
	}
	if environmentSet && !api.ValidProjectEnvironmentSlug(*environment) {
		return printErr("Invalid default environment", errors.New("--environment requires a valid nonempty environment slug; use --clear-environment to remove it"))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	before, path, err := linkedProjectContext(cwd)
	if err != nil {
		return printErr("Could not update project context", fmt.Errorf("%w; link the checkout before changing defaults", err))
	}
	if selection != nil && (selection.Link != before || selection.Path != path) {
		return printErr("Context changed during preset selection", errors.New("review the current link before applying the preset"))
	}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return printErr("Could not inspect project context directory", err)
	}
	defer parent.Close()
	if parent.root == nil {
		return printErr("Project context disappeared", errors.New("link the checkout again"))
	}
	info, err := parent.root.Lstat(filepath.Base(path))
	if err != nil {
		return printErr("Could not inspect context file", err)
	}
	if !info.Mode().IsRegular() {
		return printErr("Invalid context file", errors.New("context updates require a regular file, not a symlink"))
	}
	after := before
	if appSet {
		after.App = *app
	}
	if environmentSet {
		after.Environment = *environment
	}
	if *clearApp {
		after.App = ""
	}
	if *clearEnvironment {
		after.Environment = ""
	}
	if err := after.validate(); err != nil {
		return printErr("Invalid context update", err)
	}
	if (appSet || environmentSet) && before.Project == "" {
		if err := validatePresetUpdateTargets(after, appSet, environmentSet); err != nil {
			return printErr("Could not validate saved app", err)
		}
	}
	if (appSet || environmentSet) && before.Project != "" {
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
		defer cancel()
		project, err := client.GetProject(ctx, before.Project)
		if err != nil {
			return printErr("Could not validate linked project", err)
		}
		if project.Slug != before.Project {
			return printErr("Project identity differs", errors.New("server did not identify the linked project"))
		}
		if appSet {
			found := false
			for _, workload := range project.Workloads {
				if workload.Slug == after.App {
					found = true
					break
				}
			}
			if !found {
				return printErr("App is outside the linked project", errors.New("select an app listed by projects info for this linked project"))
			}
		}
		if environmentSet {
			selected, err := client.GetProjectEnvironment(ctx, before.Project, after.Environment)
			if err != nil {
				return printErr("Could not validate selected environment", err)
			}
			if selected.Slug != after.Environment || project.ID == "" || selected.ProjectID != project.ID {
				return printErr("Environment identity differs", errors.New("server did not identify the requested environment"))
			}
		}
		if err := ctx.Err(); err != nil {
			return printErr("Context update stopped before saving", err)
		}
	}
	current, currentPath, err := linkedProjectContext(cwd)
	if err != nil {
		return printErr("Could not re-read project context", err)
	}
	latest, err := parent.root.Lstat(filepath.Base(path))
	if err != nil {
		return printErr("Could not recheck context file", err)
	}
	if current != before || currentPath != path || !os.SameFile(info, latest) {
		return printErr("Context changed during validation", errors.New("review gregale context and repeat the intended update"))
	}
	if after != before && !*dryRun {
		if _, err := saveProjectContext(filepath.Dir(filepath.Dir(path)), after); err != nil {
			return printErr("Could not save context update", err)
		}
	}
	receipt := contextUpdateReceipt{Before: before, After: after, Path: path, Changed: after != before && !*dryRun, DryRun: *dryRun, WouldChange: after != before, Connection: effectiveConnectionContext()}
	if selection != nil {
		receipt.Preset, receipt.PresetPath = selection.Preset, selection.PresetPath
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	change := "updated"
	if !receipt.Changed {
		change = "unchanged"
	}
	if *dryRun {
		change = "would remain unchanged"
		if receipt.WouldChange {
			change = "would update"
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Local project defaults %s: %s\nProject: %s\nApp: %s -> %s\nEnvironment: %s -> %s\n", change, oneLine(path), oneLine(before.Project), contextDefaultLabel(before.App), contextDefaultLabel(after.App), contextDefaultLabel(before.Environment), contextDefaultLabel(after.Environment))
	if *dryRun {
		unchanged := []string{"project"}
		if before.App == after.App {
			unchanged = append(unchanged, "app")
		}
		if before.Environment == after.Environment {
			unchanged = append(unchanged, "environment")
		}
		_, _ = fmt.Fprintf(osStdout, "Unchanged settings: %s\nPreview only; no local files or remote services were changed.\n", strings.Join(unchanged, ", "))
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "Use gregale context to review targeting. Remote services and connection settings were not changed.")
	return 0
}

func contextDefaultLabel(value string) string {
	if value == "" {
		return "(not set)"
	}
	return oneLine(value)
}
