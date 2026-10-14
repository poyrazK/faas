package main

import (
	"fmt"
	"strings"
)

type requiredSecretPreview struct {
	Path           string   `json:"path"`
	SourceFile     string   `json:"source_file,omitempty"`
	Operation      string   `json:"operation"`
	DryRun         bool     `json:"dry_run"`
	Changed        bool     `json:"changed"`
	WouldChange    bool     `json:"would_change"`
	BeforeDeclared bool     `json:"before_declared"`
	Before         []string `json:"before"`
	After          []string `json:"after"`
	Added          []string `json:"added"`
	Removed        []string `json:"removed"`
}

func renderRequiredSecretPreview(plan requiredSecretPreview) int {
	if jsonOutput {
		return jsonOut(writeJSON(plan))
	}
	before := strings.Join(plan.Before, ", ")
	if !plan.BeforeDeclared {
		before = "not declared; completeness unknown"
	} else if len(plan.Before) == 0 {
		before = "explicitly empty declaration"
	}
	after := strings.Join(plan.After, ", ")
	if len(plan.After) == 0 {
		after = "explicitly empty declaration (the file would remain)"
	}
	_, _ = fmt.Fprintf(osStdout, "Required-name %s preview; no files changed.\nPath: %s\nBefore: %s\nAfter: %s\n", plan.Operation, oneLine(plan.Path), before, after)
	if plan.SourceFile != "" {
		_, _ = fmt.Fprintf(osStdout, "Names imported from: %s\n", oneLine(plan.SourceFile))
	}
	if len(plan.Added) > 0 {
		_, _ = fmt.Fprintf(osStdout, "Added: %s\n", strings.Join(plan.Added, ", "))
	}
	if len(plan.Removed) > 0 {
		_, _ = fmt.Fprintf(osStdout, "Removed: %s\n", strings.Join(plan.Removed, ", "))
	}
	if !plan.WouldChange {
		_, _ = fmt.Fprintln(osStdout, "The declaration already matches; no change is needed.")
	}
	return 0
}

func requiredSecretNameDifference(names, other []string) []string {
	seen := map[string]bool{}
	for _, name := range other {
		seen[name] = true
	}
	difference := []string{}
	for _, name := range names {
		if !seen[name] {
			difference = append(difference, name)
			seen[name] = true
		}
	}
	return difference
}
