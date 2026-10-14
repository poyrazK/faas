package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type envRequiredConfiguration struct {
	Declared                 bool     `json:"declared"`
	File                     string   `json:"file,omitempty"`
	Coverage                 string   `json:"coverage"`
	Missing                  []string `json:"missing"`
	Empty                    []string `json:"empty_proposed_values"`
	ExistingValuesUnverified []string `json:"existing_values_unverified"`
}

func envPushRequiredKeys() ([]string, string, error) {
	file, err := discoverRequiredSecretFile()
	if err != nil {
		return nil, "", fmt.Errorf("discover required keys: %w", err)
	}
	if file == "" {
		return nil, "", nil
	}
	keys, err := readRequiredSecretKeys(file)
	if err != nil {
		return nil, "", fmt.Errorf("read required keys: %w", err)
	}
	return sortedUniqueStrings(keys), file, nil
}

// Match the upload's scan exclusions and sequential writes. Retain only a
// nonempty boolean for each final proposed value; never return actual values.
func evaluateEnvRequiredKeys(required []string, file, scope string, pairs []secretsPair, dropped map[int]bool, metadata api.AppSecretListResponse, maxBytes int) *envRequiredConfiguration {
	report := &envRequiredConfiguration{Declared: file != "", File: file, Coverage: "unknown", Missing: []string{}, Empty: []string{}, ExistingValuesUnverified: []string{}}
	if !report.Declared {
		return report
	}
	existing := map[string]bool{}
	for _, row := range metadata.Secrets {
		if scopeOrDefault(row.Scope) == scope {
			existing[row.Key] = true
		}
	}
	proposed := map[string]bool{}
	for i, pair := range pairs {
		if !dropped[i] && api.ValidateSecretKey(pair.Key) == nil && len(pair.Value) <= maxBytes {
			proposed[pair.Key] = pair.Value != ""
		}
	}
	for _, key := range required {
		nonempty, supplied := proposed[key]
		switch {
		case supplied && !nonempty:
			report.Empty = append(report.Empty, key)
		case supplied:
		case existing[key]:
			report.ExistingValuesUnverified = append(report.ExistingValuesUnverified, key)
		default:
			report.Missing = append(report.Missing, key)
		}
	}
	report.Coverage = "keys_covered"
	if len(report.Missing) > 0 || len(report.Empty) > 0 {
		report.Coverage = "needs_attention"
	}
	return report
}

func renderEnvRequiredConfiguration(report *envRequiredConfiguration) {
	if !report.Declared {
		_, _ = fmt.Fprintln(osStdout, "Required configuration: unknown; no project declaration was found.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Required declaration: %s\n", oneLine(report.File))
	switch report.Coverage {
	case "needs_attention":
		_, _ = fmt.Fprintln(osStdout, "Required configuration: needs attention before upload.")
	default:
		_, _ = fmt.Fprintln(osStdout, "Required key coverage: all declared names would be present after a successful upload.")
	}
	if len(report.Missing) > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Missing required keys: %s\n", strings.Join(report.Missing, ", "))
	}
	if len(report.Empty) > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Empty required values: %s\n", strings.Join(report.Empty, ", "))
	}
	if len(report.ExistingValuesUnverified) > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Existing values remain unverified: %s\n", strings.Join(report.ExistingValuesUnverified, ", "))
	}
	_, _ = fmt.Fprintln(osStdout, "Coverage checks names and proposed nonempty values; existing values, delivery, and application behavior are not verified.")
}
