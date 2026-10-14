package main

import (
	"errors"
	"flag"
	"fmt"
)

func cmdContextPresetDelete(args []string) int {
	fs := newFlagSet("context presets delete", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview deleting a preset without writing")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		return printErr("Invalid deletion arguments", errors.New("use context presets delete NAME [--dry-run]"))
	}
	name := fs.Arg(0)
	if err := validateProfileName(name); err != nil {
		return printErr("Invalid preset name", err)
	}
	data, path, err := loadContextPresets()
	if err != nil {
		return printErr("Could not read context presets", err)
	}
	preset, exists := data.Presets[name]
	if !exists {
		return unknownSavedName("Unknown context preset", name, data.Presets, func(candidate string) []string {
			return correctedSavedNameFlags([]string{"context", "presets", "delete"}, fs, candidate, "")
		}, []string{"context", "presets", "list"})
	}
	current, currentPath, err := loadContextPresets()
	if err != nil {
		return printErr("Could not re-read context presets", err)
	}
	if currentPath != path || !sameContextPresets(current, data) {
		return printErr("Saved presets changed", errors.New("review context presets list and retry the deletion"))
	}
	if *dryRun {
		if jsonOutput {
			return jsonOut(writeJSON(struct {
				Name                 string              `json:"name"`
				Preset               localProjectContext `json:"preset"`
				Path                 string              `json:"path"`
				DryRun               bool                `json:"dry_run"`
				Changed              bool                `json:"changed"`
				WouldChange          bool                `json:"would_change"`
				ActiveContextChanged bool                `json:"active_context_changed"`
			}{name, preset, path, true, false, true, false}))
		}
		_, _ = fmt.Fprintf(osStdout, "Delete context preset preview; no files changed.\nWould remove %s: project=%s; app=%s; environment=%s\nPath: %s\nActive context and other presets would remain unchanged.\n", name, preset.Project, contextDefaultLabel(preset.App), contextDefaultLabel(preset.Environment), oneLine(path))
		return 0
	}
	delete(data.Presets, name)
	if err := writeContextPresets(path, data); err != nil {
		return printErr("Could not delete context preset", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Deleted string `json:"deleted"`
			Path    string `json:"path"`
		}{name, path}))
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted local context preset %s. Linked targets were not changed.\n", name)
	return 0
}
