package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func previewClearLogDefaults(path string) int {
	report := struct {
		Path        string            `json:"path"`
		DryRun      bool              `json:"dry_run"`
		Changed     bool              `json:"changed"`
		Cleared     bool              `json:"cleared"`
		WouldChange bool              `json:"would_change"`
		BeforeSaved bool              `json:"before_saved"`
		Before      *localLogDefaults `json:"before"`
		After       *localLogDefaults `json:"after"`
		Effects     []string          `json:"effects"`
	}{Path: path, DryRun: true, Effects: []string{}}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return printErr("Could not inspect log defaults directory", err)
	}
	defer parent.Close()
	if parent.root != nil {
		info, err := parent.root.Lstat(filepath.Base(path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not inspect log defaults", err)
		}
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > 8192 {
				return printErr("Invalid log defaults file", errors.New("preview requires a regular, nonsymlink file within 8 KiB"))
			}
			before, foundPath, err := readLogDefaults()
			if err != nil || foundPath != path {
				return printErr("Could not preview clearing log defaults", errors.New("existing defaults could not be safely read or validated; inspect the file before retrying"))
			}
			report.Before, report.BeforeSaved, report.WouldChange = &before, true, true
			report.Effects = []string{"The project will have no preferred log view; logs --saved-view will require a new preference.", "Saved views remain available through logs --view NAME."}
		}
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Clear log defaults preview; no files changed.\nPath: %s\n", oneLine(path))
	if !report.BeforeSaved {
		_, _ = fmt.Fprintln(osStdout, "No preferred saved view; nothing to clear.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Would remove preferred view: %s\nAfter: no preferred saved view\n", report.Before.View)
	for _, effect := range report.Effects {
		_, _ = fmt.Fprintln(osStdout, effect)
	}
	return 0
}
