package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Local observations only: a project link does not establish remote membership,
// credentials, or whether a command supports the linked environment.
type projectTargetingOverview struct {
	Status            string   `json:"status"`
	AppSource         string   `json:"app_source"`
	EnvironmentSource string   `json:"environment_source"`
	ContextPath       string   `json:"context_path,omitempty"`
	ConfigurationPath string   `json:"configuration_path"`
	Notes             []string `json:"notes"`
	NextCommand       string   `json:"next_command,omitempty"`
}

func projectTargeting(context localProjectContext, path, configPath string, connection *connectionContext) *projectTargetingOverview {
	overview := &projectTargetingOverview{
		Status:            "ready for linked app defaults",
		AppSource:         "linked project context",
		EnvironmentSource: "linked project context",
		ContextPath:       path,
		ConfigurationPath: configPath,
		Notes: []string{
			"Explicit app and environment flags override linked defaults only for commands that support those selectors.",
			"Linked settings are local defaults; remote existence, membership, authentication, and health are not checked.",
		},
	}
	if path == "" {
		overview.Status = "no linked project"
		overview.AppSource = "not set"
		overview.EnvironmentSource = "command default"
		overview.Notes = append(overview.Notes, "No project context was found in this directory or its ancestors.")
		overview.NextCommand = recoveryCLI() + " project list"
	} else {
		overview.Notes = append(overview.Notes, "The nearest ancestor .gregale/project.json wins, including when running from a subdirectory.")
		if context.App == "" {
			overview.Status = "default app missing"
			overview.AppSource = "not set"
			overview.Notes = append(overview.Notes, "App-scoped commands need an explicit app or a link with --app; no workload is inferred here.")
			overview.NextCommand = recoveryCLI() + " project get " + recoveryArgument(context.Project)
		}
		if context.Project == "" {
			overview.AppSource = "saved standalone app"
			overview.Notes = append(overview.Notes, "This app has no saved project. Use context set --app to change it, or unlink to remove the default.")
		}
		if context.Environment == "" {
			overview.EnvironmentSource = "command default"
			overview.Notes = append(overview.Notes, "No environment is linked. Scoped secrets use default; other commands use their own defaults. A connection profile is not a project environment.")
		} else {
			overview.Notes = append(overview.Notes, "The linked environment applies only to environment-aware commands; it does not scope every app read.")
		}
	}
	if connection != nil && strings.HasPrefix(connection.APISource, "env:") {
		overview.Notes = append(overview.Notes, "FAAS_API overrides the selected profile's API address; the profile still selects its stored credentials.")
	}
	return overview
}

func renderProjectTargeting(overview *projectTargetingOverview) {
	_, _ = fmt.Fprintf(osStdout, "Targeting: %s\nApp source: %s\nEnvironment source: %s\n", overview.Status, overview.AppSource, overview.EnvironmentSource)
	if overview.ContextPath != "" {
		_, _ = fmt.Fprintf(osStdout, "Context file (absolute): %s\n", oneLine(overview.ContextPath))
	}
	_, _ = fmt.Fprintf(osStdout, "Connection configuration: %s\n", oneLine(overview.ConfigurationPath))
	for _, note := range overview.Notes {
		_, _ = fmt.Fprintln(osStdout, note)
	}
	if overview.NextCommand != "" {
		_, _ = fmt.Fprintf(osStdout, "Next step (POSIX shells):\n  %s\n", overview.NextCommand)
	}
}

func absoluteContextConfigPath() (string, error) {
	path, err := cliConfigPath()
	if err != nil {
		return "", fmt.Errorf("locate connection configuration: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve connection configuration path: %w", err)
	}
	return abs, nil
}
