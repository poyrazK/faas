package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type jobDefaultsPreview struct {
	Path        string            `json:"path"`
	DryRun      bool              `json:"dry_run"`
	Changed     bool              `json:"changed"`
	WouldChange bool              `json:"would_change"`
	BeforeSaved bool              `json:"before_saved"`
	Before      *localJobDefaults `json:"before"`
	After       localJobDefaults  `json:"after"`
}

func sameJobDefaults(a, b localJobDefaults) bool {
	if len(a.Include) == 0 {
		a.Include = nil
	}
	if len(a.Exclude) == 0 {
		a.Exclude = nil
	}
	if len(b.Include) == 0 {
		b.Include = nil
	}
	if len(b.Exclude) == 0 {
		b.Exclude = nil
	}
	return reflect.DeepEqual(a, b)
}

func previewJobDefaults(path string, proposed localJobDefaults, expected *localJobDefaults) int {
	report := jobDefaultsPreview{Path: path, DryRun: true, WouldChange: true, After: proposed}
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
				return printErr("Could not preview Job defaults", errors.New("existing defaults could not be read or validated; review them before previewing"))
			}
			if foundPath != path {
				return printErr("Defaults changed", errors.New("the defaults root changed; retry the preview"))
			}
			report.Before, report.BeforeSaved = &before, true
			report.WouldChange = !sameJobDefaults(before, proposed)
		}
	}
	if expected != nil && (report.Before == nil || !sameJobDefaults(*expected, *report.Before)) {
		return printErr("Defaults changed", errors.New("saved defaults changed while preparing the preview; retry"))
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(report))
	}
	_, _ = fmt.Fprintf(osStdout, "Job defaults preview; no files changed and no run submitted.\nPath: %s\n", oneLine(path))
	if report.BeforeSaved {
		_, _ = fmt.Fprintf(osStdout, "Before: %s\n", jobDefaultsPreviewSummary(*report.Before))
	} else {
		_, _ = fmt.Fprintln(osStdout, "Before: no saved Job defaults")
	}
	_, _ = fmt.Fprintf(osStdout, "After: %s\n", jobDefaultsPreviewSummary(proposed))
	if !report.WouldChange {
		_, _ = fmt.Fprintln(osStdout, "Saved settings already match; no change is needed.")
	}
	return 0
}

func jobDefaultsPreviewSummary(d localJobDefaults) string {
	tasks := fmt.Sprintf("%d", d.Tasks)
	if d.Tasks == 0 {
		tasks = "unset"
	}
	filters := "all artifacts"
	if len(d.Include) > 0 || len(d.Exclude) > 0 {
		filters = fmt.Sprintf("include=%s; exclude=%s", oneLine(strings.Join(d.Include, ", ")), oneLine(strings.Join(d.Exclude, ", ")))
	}
	return fmt.Sprintf("Job=%s; tasks=%s; results=%t; wait-ready=%t; %s", oneLine(d.Job), tasks, d.Results, d.WaitReady, filters)
}
