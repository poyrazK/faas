package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type localConfigEntry struct {
	Name           string               `json:"name"`
	Path           string               `json:"path"`
	Status         string               `json:"status"`
	Summary        string               `json:"summary"`
	InspectCommand string               `json:"inspect_command"`
	RepairCommand  string               `json:"repair_command,omitempty"`
	Reference      *logDefaultReference `json:"reference,omitempty"`
}

type projectLocalConfig struct {
	Root            string               `json:"root"`
	Valid           bool                 `json:"valid"`
	Files           []localConfigEntry   `json:"files"`
	RecoveryRecords *localRecoveryConfig `json:"recovery_records,omitempty"`
	ProblemsOnly    bool                 `json:"problems_only,omitempty"`
}

func cmdContextLocalConfig(problemsOnly bool) int {
	defaultsPath, err := jobDefaultsPath()
	if err != nil {
		return printErr("Could not resolve local configuration root", errors.New("project context could not be read; inspect the nearest .gregale/project.json before retrying"))
	}
	directory := filepath.Dir(defaultsPath)
	report := projectLocalConfig{Root: filepath.Dir(directory), Valid: true, Files: []localConfigEntry{}}
	command := "gregale --profile " + recoveryArgument(currentProfile()) + " "
	specs := []struct {
		name, file, inspect, repair string
		limit                       int64
	}{
		{"Job defaults", "jobs.json", "jobs defaults show", "jobs defaults clear", 65536},
		{"Secret comparison scopes", "secrets.json", "secrets defaults show", "secrets defaults clear", 8192},
		{"Required secret names", "required-secrets.json", "secrets requirements show", "secrets requirements clear", 65536},
		{"Context presets", "context-presets.json", "context presets list", "", 65536},
		{"Log view defaults", "logs.json", "logs defaults show", "logs defaults clear", 8192},
		{"Workflow recovery defaults", "workflows.json", "workflows defaults show", "workflows defaults clear", 8192},
	}
	parent, parentErr := inspectArtifactOutputDirectory(directory)
	if parentErr == nil {
		defer parent.Close()
	}
	for _, spec := range specs {
		row := localConfigEntry{Name: spec.name, Path: filepath.Join(directory, spec.file), Status: "missing", Summary: "Optional configuration is not saved.", InspectCommand: command + spec.inspect}
		var readErr error
		canClear := false
		switch {
		case parentErr != nil:
			readErr = parentErr
		case parent.root == nil:
		case parent.root != nil:
			info, err := parent.root.Lstat(spec.file)
			if err == nil {
				canClear = info.Mode().IsRegular()
			}
			switch {
			case errors.Is(err, os.ErrNotExist):
			case err != nil:
				readErr = err
			case !info.Mode().IsRegular() || info.Size() > spec.limit:
				readErr = errors.New("configuration must be a regular file within its size limit")
			default:
				row.Status = "configured"
				if spec.file == "logs.json" {
					defaults, path, err := readLogDefaults()
					readErr = err
					if readErr == nil && path != row.Path {
						readErr = errors.New("configuration root changed")
					}
					if readErr == nil {
						row.Summary = fmt.Sprintf("Preferred saved log view %s.", oneLine(defaults.View))
						reference := inspectLogDefaultReference(defaults.View)
						row.Reference = &reference
						if reference.Status != "available" {
							row.Status = "broken_reference"
							report.Valid = false
						}
					}
				} else {
					row.Summary, readErr = localConfigFileSummary(spec.file, row.Path)
				}
			}
		}
		if readErr != nil {
			report.Valid = false
			row.Status = "invalid"
			row.Summary = "File or directory could not be safely read or validated; contents were not displayed."
			if canClear && spec.repair != "" {
				row.RepairCommand = command + spec.repair
			}
		}
		if spec.file == "required-secrets.json" && row.Status == "missing" {
			row.Summary = "Required names are not declared; completeness is unknown."
		}
		report.Files = append(report.Files, row)
	}
	recovery := inspectLocalRecoveryConfig(filepath.Join(directory, "accepted-runs"), command)
	report.RecoveryRecords = &recovery
	if report.RecoveryRecords.Status == "invalid" {
		report.Valid = false
	}
	if problemsOnly {
		report.ProblemsOnly = true
		problems := []localConfigEntry{}
		for _, row := range report.Files {
			if row.Status == "invalid" || row.Status == "broken_reference" {
				problems = append(problems, row)
			}
		}
		report.Files = problems
		if recovery.Status != "invalid" {
			report.RecoveryRecords = nil
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Project configuration (offline)\nRoot: %s\n", oneLine(report.Root))
		for _, row := range report.Files {
			_, _ = fmt.Fprintf(osStdout, "%s: %s\n  Path: %s\n  %s\n  Inspect (POSIX shells): %s\n", row.Name, row.Status, oneLine(row.Path), row.Summary, row.InspectCommand)
			if row.Reference != nil {
				_, _ = fmt.Fprintf(osStdout, "  Local reference: %s\n  Saved-view file: %s\n  %s\n", row.Reference.Status, oneLine(row.Reference.Path), row.Reference.Message)
				if row.Reference.Status != "available" {
					_, _ = fmt.Fprintf(osStdout, "  Inspect saved views (POSIX shells): %s\n", row.Reference.InspectCommand)
				}
				if row.Reference.ReconfigureCommandTemplate != "" {
					_, _ = fmt.Fprintf(osStdout, "  Choose another view (template; replace NAME with an existing view): %s\n", row.Reference.ReconfigureCommandTemplate)
				}
			}
			if row.RepairCommand != "" {
				_, _ = fmt.Fprintf(osStdout, "  To discard this local configuration explicitly (POSIX shells): %s\n", row.RepairCommand)
			} else if row.Status == "invalid" {
				_, _ = fmt.Fprintln(osStdout, "  Review the file and directory at the path above; no automatic repair was attempted.")
			}
		}
		if report.RecoveryRecords != nil {
			renderLocalRecoveryConfig(*report.RecoveryRecords)
		}
		if problemsOnly && report.Valid {
			_, _ = fmt.Fprintln(osStdout, "No local configuration problems found.")
		}
		if !problemsOnly {
			_, _ = fmt.Fprintln(osStdout, "Missing optional files do not fail this summary. Remote resources, credentials, and health were not checked.")
		} else {
			_, _ = fmt.Fprintln(osStdout, "Remote resources, credentials, and health were not checked.")
		}
	}
	if !report.Valid {
		return 1
	}
	return 0
}

func localConfigFileSummary(file, expectedPath string) (string, error) {
	switch file {
	case "workflows.json":
		defaults, path, err := readWorkflowDefaults()
		if err == nil && path != expectedPath {
			err = errors.New("configuration root changed")
		}
		return fmt.Sprintf("Preferred workflow %s on recorded app %s; target availability was not checked.", oneLine(defaults.Workflow), defaults.App), err
	case "jobs.json":
		defaults, path, err := readJobDefaults()
		if err == nil && path != expectedPath {
			err = errors.New("configuration root changed")
		}
		return fmt.Sprintf("Saved Job defaults for %s.", oneLine(defaults.Job)), err
	case "secrets.json":
		defaults, path, err := readSecretDefaults()
		if err == nil && path != expectedPath {
			err = errors.New("configuration root changed")
		}
		return fmt.Sprintf("%d saved comparison scopes.", len(defaults.Environments)), err
	case "required-secrets.json":
		keys, err := readRequiredSecretKeys(expectedPath)
		if err != nil {
			return "", err
		}
		if len(keys) == 0 {
			return "Explicitly empty required-name declaration.", nil
		}
		return fmt.Sprintf("%d distinct required names.", len(requiredSecretNameDifference(keys, nil))), nil
	case "context-presets.json":
		data, path, err := loadContextPresets()
		if err == nil && path != expectedPath {
			err = errors.New("configuration root changed")
		}
		return fmt.Sprintf("%d saved target presets.", len(data.Presets)), err
	default:
		return "", errors.New("unknown local configuration file")
	}
}
