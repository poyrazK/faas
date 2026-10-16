package main

import (
	"errors"
	"fmt"
	"os"
)

type contextPresetListEntry struct {
	Name           string              `json:"name"`
	Preset         localProjectContext `json:"preset"`
	Status         string              `json:"status"`
	MatchesCurrent bool                `json:"matches_current"`
	PreviewCommand string              `json:"preview_command"`
	RelinkCommand  string              `json:"relink_command,omitempty"`
}

func listContextPresets(data contextPresetsFile, path string, names []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	current, _, err := linkedProjectContext(cwd)
	linked := err == nil
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		return printErr("Could not inspect active context", err)
	}
	prefix := "gregale --profile " + recoveryArgument(currentProfile())
	entries := []contextPresetListEntry{}
	for _, name := range names {
		preset := data.Presets[name]
		entry := contextPresetListEntry{Name: name, Preset: preset, Status: "different_targets", PreviewCommand: prefix + " context use " + recoveryArgument(name) + " --dry-run"}
		switch {
		case !linked || preset.Project != current.Project:
			entry.Status = "relink_required"
			entry.RelinkCommand = prefix + " link " + recoveryArgument(preset.Project)
			if preset.App != "" {
				entry.RelinkCommand += " --app " + recoveryArgument(preset.App)
			}
			if preset.Environment != "" {
				entry.RelinkCommand += " --environment " + recoveryArgument(preset.Environment)
			}
		case preset == current:
			entry.Status = "matches_current_targets"
			entry.MatchesCurrent = true
		}
		entries = append(entries, entry)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Path    string                         `json:"path"`
			Presets map[string]localProjectContext `json:"presets"`
			Entries []contextPresetListEntry       `json:"entries"`
		}{path, data.Presets, entries}))
	}
	_, _ = fmt.Fprintf(osStdout, "Preset source: %s\n", oneLine(path))
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No saved context presets. Use context save NAME after linking targets.")
	}
	for _, entry := range entries {
		preset := entry.Preset
		_, _ = fmt.Fprintf(osStdout, "%s: project=%s; app=%s; environment=%s\n", entry.Name, preset.Project, contextDefaultLabel(preset.App), contextDefaultLabel(preset.Environment))
		switch entry.Status {
		case "matches_current_targets":
			_, _ = fmt.Fprintln(osStdout, "  Matches current linked targets.")
		case "different_targets":
			_, _ = fmt.Fprintln(osStdout, "  Different targets in the linked project.")
		default:
			_, _ = fmt.Fprintf(osStdout, "  Relinking is required before preview. Relinking changes local defaults (POSIX shells):\n    %s\n", entry.RelinkCommand)
		}
		_, _ = fmt.Fprintf(osStdout, "  Preview (POSIX shells):\n    %s\n", entry.PreviewCommand)
	}
	_, _ = fmt.Fprintln(osStdout, "Matches compare local target names only. Listing does not validate remote targets or execute any command.")
	return 0
}
