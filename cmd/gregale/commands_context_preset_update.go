package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type contextPresetUpdateReceipt struct {
	Name                 string              `json:"name"`
	Before               localProjectContext `json:"before"`
	After                localProjectContext `json:"after"`
	Path                 string              `json:"path"`
	DryRun               bool                `json:"dry_run,omitempty"`
	WouldChange          bool                `json:"would_change"`
	Changed              bool                `json:"changed"`
	ActiveContextChanged bool                `json:"active_context_changed"`
}

func cmdContextPresetUpdate(args []string) int {
	if len(args) == 0 {
		return printErr("Missing preset name", errors.New("use context presets update NAME with target or clear options"))
	}
	name := args[0]
	if err := validateProfileName(name); err != nil {
		return printErr("Invalid preset name", err)
	}
	data, path, err := loadContextPresets()
	if err != nil {
		return printErr("Could not read presets", err)
	}
	before, exists := data.Presets[name]
	if !exists {
		return unknownSavedName("Unknown context preset", name, data.Presets, func(candidate string) []string {
			return append([]string{"context", "presets", "update", candidate}, args[1:]...)
		}, []string{"context", "presets", "list"})
	}
	after := before
	fs := newFlagSet("context presets update", flag.ContinueOnError)
	app := fs.String("app", "", "replace only the preset app")
	environment := fs.String("environment", "", "replace only the preset environment")
	clearApp := fs.Bool("clear-app", false, "remove the preset app")
	clearEnvironment := fs.Bool("clear-environment", false, "remove the preset environment")
	dryRun := fs.Bool("dry-run", false, "validate and show proposed preset without saving")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	appSet, environmentSet := logsFlagWasSet(fs, "app"), logsFlagWasSet(fs, "environment")
	if !appSet && !environmentSet && !*clearApp && !*clearEnvironment || appSet && *clearApp || environmentSet && *clearEnvironment {
		return printErr("Invalid preset update", errors.New("select a target or clear option; setting and clearing the same field cannot be combined"))
	}
	if appSet && !api.ValidAppSlug(*app) {
		return printErr("Invalid preset app", errors.New("provide a valid nonempty app slug; use --clear-app to remove it"))
	}
	if environmentSet && !api.ValidProjectEnvironmentSlug(*environment) {
		return printErr("Invalid preset environment", errors.New("provide a valid nonempty environment slug; use --clear-environment to remove it"))
	}
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
	if appSet || environmentSet {
		if err := validatePresetUpdateTargets(after, appSet, environmentSet); err != nil {
			return printErr("Could not validate preset targets", err)
		}
	}
	current, currentPath, err := loadContextPresets()
	if err != nil {
		return printErr("Could not re-read presets", err)
	}
	if currentPath != path || !sameContextPresets(current, data) {
		return printErr("Presets changed during validation", errors.New("review the saved preset and repeat the intended update"))
	}
	changed := after != before
	if changed && !*dryRun {
		data.Presets[name] = after
		if err := writeContextPresets(path, data); err != nil {
			return printErr("Could not save preset update", err)
		}
	}
	receipt := contextPresetUpdateReceipt{Name: name, Before: before, After: after, Path: path, DryRun: *dryRun, WouldChange: changed, Changed: changed && !*dryRun}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "Preset %s: saved change=%t; would change=%t; preview=%t\nProject preserved: %s\nApp: %s -> %s\nEnvironment: %s -> %s\nPath: %s\nActive context was not changed.\n", name, receipt.Changed, receipt.WouldChange, receipt.DryRun, before.Project, contextDefaultLabel(before.App), contextDefaultLabel(after.App), contextDefaultLabel(before.Environment), contextDefaultLabel(after.Environment), oneLine(path))
	return 0
}

func validatePresetUpdateTargets(target localProjectContext, appSet, environmentSet bool) error {
	client, err := authedClient()
	if err != nil {
		return fmt.Errorf("authenticate current connection: %w", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	if target.Project == "" {
		if err := target.validate(); err != nil {
			return err
		}
		return verifyStandaloneContextApp(ctx, client, target.App)
	}
	project, err := client.GetProject(ctx, target.Project)
	if err != nil {
		return fmt.Errorf("read preset project: %w", err)
	}
	if project.Slug != target.Project {
		return errors.New("server did not identify the preset project")
	}
	if appSet {
		found := false
		for _, workload := range project.Workloads {
			if workload.Slug == target.App {
				found = true
				break
			}
		}
		if !found {
			return errors.New("selected app is not a workload in the preset project")
		}
	}
	if environmentSet {
		environment, err := client.GetProjectEnvironment(ctx, target.Project, target.Environment)
		if err != nil {
			return fmt.Errorf("read preset environment: %w", err)
		}
		if environment.Slug != target.Environment || project.ID == "" || environment.ProjectID != project.ID {
			return errors.New("environment response did not match the preset project and environment")
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("preset validation stopped: %w", err)
	}
	return nil
}

func sameContextPresets(a, b contextPresetsFile) bool {
	if a.Version != b.Version || len(a.Presets) != len(b.Presets) {
		return false
	}
	for name, preset := range a.Presets {
		other, ok := b.Presets[name]
		if !ok || preset != other {
			return false
		}
	}
	return true
}
