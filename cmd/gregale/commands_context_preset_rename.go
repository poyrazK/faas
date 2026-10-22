package main

import (
	"errors"
	"fmt"
)

func cmdContextPresetRename(args []string) int {
	return cmdContextPresetTransfer(args, false)
}

func cmdContextPresetCopy(args []string) int {
	return cmdContextPresetTransfer(args, true)
}

func cmdContextPresetTransfer(args []string, copying bool) int {
	operation, past := "rename", "Renamed"
	if copying {
		operation, past = "copy", "Copied"
	}
	if len(args) != 2 {
		return printErr("Invalid preset arguments", fmt.Errorf("use context presets %s OLD_NAME NEW_NAME", operation))
	}
	oldName, newName := args[0], args[1]
	for _, name := range args {
		if err := validateProfileName(name); err != nil {
			return printErr("Invalid preset name", err)
		}
	}
	if oldName == newName {
		return printErr("Names are identical", errors.New("choose a different destination name; no preset was changed"))
	}
	data, path, err := loadContextPresets()
	if err != nil {
		return printErr("Could not read context presets", err)
	}
	preset, exists := data.Presets[oldName]
	if !exists {
		return unknownSavedName("Unknown context preset", oldName, data.Presets, func(candidate string) []string { return []string{"context", "presets", operation, candidate, newName} }, []string{"context", "presets", "list"})
	}
	if _, exists := data.Presets[newName]; exists {
		return printErr("Destination preset exists", errors.New("choose an unused name; existing presets are never overwritten"))
	}
	current, currentPath, err := loadContextPresets()
	if err != nil {
		return printErr("Could not re-read context presets", err)
	}
	if currentPath != path || !sameContextPresets(current, data) {
		return printErr("Saved presets changed", fmt.Errorf("review context presets list and retry the %s", operation))
	}
	if !copying {
		delete(data.Presets, oldName)
	}
	data.Presets[newName] = preset
	if err := writeContextPresets(path, data); err != nil {
		return printErr("Could not "+operation+" context preset", err)
	}
	command := "gregale --profile " + recoveryArgument(currentProfile()) + " context presets show " + recoveryArgument(newName)
	receipt := struct {
		Operation            string              `json:"operation"`
		SourcePreserved      bool                `json:"source_preserved"`
		From                 string              `json:"from"`
		To                   string              `json:"to"`
		Preset               localProjectContext `json:"preset"`
		Path                 string              `json:"path"`
		ActiveContextChanged bool                `json:"active_context_changed"`
		InspectCommand       string              `json:"inspect_command"`
	}{operation, copying, oldName, newName, preset, path, false, command}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "%s context preset %s -> %s\nTargets preserved: project=%s; app=%s; environment=%s\nPath: %s\nActive context unchanged.\nInspect (POSIX shells):\n  %s\n", past, oldName, newName, preset.Project, contextDefaultLabel(preset.App), contextDefaultLabel(preset.Environment), oneLine(path), command)
	return 0
}
