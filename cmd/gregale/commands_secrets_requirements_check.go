package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type requiredSecretConsistency struct {
	Path                string   `json:"path"`
	SourceFile          string   `json:"source_file"`
	SourceSelection     string   `json:"source_selection"`
	Declared            bool     `json:"declared"`
	Consistent          bool     `json:"consistent"`
	RequiredNames       []string `json:"required_names"`
	ExampleNames        []string `json:"example_names"`
	MissingRequirements []string `json:"missing_requirements"`
	ExtraRequirements   []string `json:"extra_requirements"`
	MergeCommand        string   `json:"merge_command,omitempty"`
	ReviewRemoveCommand string   `json:"review_remove_command,omitempty"`
}

func cmdSecretsRequiredConsistency(args []string) int {
	fs := newFlagSet("secrets requirements check", flag.ContinueOnError)
	source := fs.String("from-env-file", "", "dotenv example (default: .env.example at the requirements project root)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	sourceSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "from-env-file" {
			sourceSet = true
		}
	})
	if sourceSet && strings.TrimSpace(*source) == "" {
		return printErr("Missing dotenv source", errors.New("--from-env-file requires a nonempty file path"))
	}
	defaultsPath, err := jobDefaultsPath()
	if err != nil {
		return printErr("Could not locate requirements", err)
	}
	path := filepath.Join(filepath.Dir(defaultsPath), "required-secrets.json")
	sourcePath := filepath.Join(filepath.Dir(filepath.Dir(defaultsPath)), ".env.example")
	selection := "project_default"
	if sourceSet {
		selection = "flag"
		sourcePath, err = filepath.Abs(*source)
		if err != nil {
			return printErr("Could not locate dotenv source", err)
		}
	}
	names, err := readRequiredSecretEnvNames(sourcePath)
	if err != nil {
		if !sourceSet && errors.Is(err, os.ErrNotExist) {
			return printErr("Project dotenv example not found", fmt.Errorf("expected %s; create that example or choose another file with %s secrets requirements check --from-env-file PATH", oneLine(sourcePath), recoveryCLI()))
		}
		return printErr("Could not read dotenv names", err)
	}
	plan, err := planRequiredSecretNames(path, "set", names)
	if err != nil {
		return printErr("Could not compare requirements", err)
	}
	report := requiredSecretConsistency{
		Path: path, SourceFile: sourcePath, SourceSelection: selection, Declared: plan.BeforeDeclared,
		RequiredNames: plan.Before, ExampleNames: plan.After,
		MissingRequirements: plan.Added, ExtraRequirements: plan.Removed,
	}
	report.Consistent = report.Declared && len(report.MissingRequirements) == 0 && len(report.ExtraRequirements) == 0
	command := "gregale --profile " + recoveryArgument(currentProfile()) + " secrets requirements "
	if len(report.MissingRequirements) > 0 {
		report.MergeCommand = command + "add --from-env-file " + recoveryArgument(sourcePath) + " --dry-run"
	}
	if len(report.ExtraRequirements) > 0 {
		report.ReviewRemoveCommand = command + "remove"
		for _, key := range report.ExtraRequirements {
			report.ReviewRemoveCommand += " " + recoveryArgument(key)
		}
		report.ReviewRemoveCommand += " --dry-run"
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Required-name consistency (offline)\nDeclaration: %s\nExample: %s\n", oneLine(path), oneLine(sourcePath))
		if report.Consistent {
			_, _ = fmt.Fprintln(osStdout, "Required names match the example; ordering and duplicates do not affect this check.")
		} else {
			if !report.Declared {
				_, _ = fmt.Fprintln(osStdout, "Required names are not declared; completeness is unknown.")
			}
			if len(report.MissingRequirements) > 0 {
				_, _ = fmt.Fprintf(osStdout, "In example but not declared: %s\nPreview adding these names (POSIX shells):\n  %s\n", strings.Join(report.MissingRequirements, ", "), report.MergeCommand)
			}
			if len(report.ExtraRequirements) > 0 {
				_, _ = fmt.Fprintf(osStdout, "Declared but absent from example: %s\nReview whether these names should remain required. Preview removal (POSIX shells):\n  %s\n", strings.Join(report.ExtraRequirements, ", "), report.ReviewRemoveCommand)
			}
		}
	}
	if !report.Consistent {
		return 1
	}
	return 0
}
