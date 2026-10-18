package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type jobDefaultsClearPreview struct {
	Path        string            `json:"path"`
	DryRun      bool              `json:"dry_run"`
	Changed     bool              `json:"changed"`
	Cleared     bool              `json:"cleared"`
	WouldChange bool              `json:"would_change"`
	BeforeSaved bool              `json:"before_saved"`
	Before      *localJobDefaults `json:"before"`
	After       *localJobDefaults `json:"after"`
	Effects     []string          `json:"effects"`
}

func previewClearJobDefaults(path string) int {
	report := jobDefaultsClearPreview{Path: path, DryRun: true, Effects: []string{}}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(path))
	if err != nil {
		return printErr("Could not inspect defaults directory", err)
	}
	defer parent.Close()
	if parent.root != nil {
		info, err := parent.root.Lstat(filepath.Base(path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not inspect Job defaults", err)
		}
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > 65536 {
				return printErr("Invalid Job defaults file", errors.New("preview requires a regular, nonsymlink file within 64 KiB"))
			}
			before, foundPath, err := readJobDefaults()
			if err != nil {
				return printErr("Could not preview clearing Job defaults", errors.New("existing defaults could not be read or validated; inspect the file before retrying"))
			}
			if foundPath != path {
				return printErr("Defaults changed", errors.New("the defaults root changed; retry the preview"))
			}
			report.Before, report.BeforeSaved, report.WouldChange = &before, true, true
			report.Effects = []string{
				"The saved Job name will no longer fill an omitted Job selector.",
				"Saved task-count, results, and image-wait preferences will no longer apply.",
				"Saved artifact filters will no longer apply to result collection.",
			}
		}
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Clear Job defaults preview; no files changed.\nPath: %s\n", oneLine(path))
	if !report.BeforeSaved {
		_, _ = fmt.Fprintln(osStdout, "No saved Job defaults; nothing to clear.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Would remove: %s\nAfter: no saved Job defaults\n", jobDefaultsPreviewSummary(*report.Before))
	for _, effect := range report.Effects {
		_, _ = fmt.Fprintln(osStdout, effect)
	}
	return 0
}
