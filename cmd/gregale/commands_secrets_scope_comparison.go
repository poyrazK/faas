package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type secretScopeColumn struct {
	Scope       string `json:"scope"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
	NextCommand string `json:"next_command,omitempty"`
}

type secretScopeRow struct {
	Key    string            `json:"key"`
	States map[string]string `json:"states"`
}

type secretScopeComparison struct {
	DefaultsFile       string              `json:"comparison_defaults_file,omitempty"`
	App                string              `json:"app_slug"`
	RequirementsSource string              `json:"requirements_source"`
	RequirementsFile   string              `json:"requirements_file,omitempty"`
	Scopes             []secretScopeColumn `json:"scopes"`
	Keys               []secretScopeRow    `json:"keys"`
}

func parseSecretComparisonScopes(raw string) ([]string, error) {
	scopes := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		scope := strings.TrimSpace(part)
		if scope == "" || scope == "__all__" || api.ValidateScope(scope) != nil {
			return nil, errors.New("provide comma-separated valid, nonempty single scope names; __all__ is not supported")
		}
		if !seen[scope] {
			seen[scope] = true
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) > 10 {
		return nil, errors.New("compare at most 10 distinct scopes at once")
	}
	return scopes, nil
}

func compareRequiredSecretScopes(app string, scopes, required []string, source, file, defaultsPath string) int {
	report := secretScopeComparison{App: app, RequirementsSource: source, RequirementsFile: file, DefaultsFile: defaultsPath, Scopes: []secretScopeColumn{}, Keys: []secretScopeRow{}}
	for _, key := range required {
		report.Keys = append(report.Keys, secretScopeRow{Key: key, States: map[string]string{}})
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	failed := false
	for _, scope := range scopes {
		column := secretScopeColumn{Scope: scope, Status: "configured"}
		var metadata api.AppSecretListResponse
		readErr := ctx.Err()
		if readErr == nil {
			readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
			metadata, readErr = client.ListSecretsWithScope(readCtx, app, scope)
			readCancel()
		}
		if readErr != nil {
			column.Status = "unverified"
			column.Message = "Scoped metadata could not be read; missing keys cannot be established."
			failed = true
		}
		for i := range report.Keys {
			state := "unverified"
			if readErr == nil {
				_, present := findSecretStatus(metadata, report.Keys[i].Key, scope)
				state = "configured"
				if !present {
					state = "missing"
					column.Status = "missing"
					failed = true
				}
			}
			report.Keys[i].States[scope] = state
		}
		if column.Status != "configured" {
			column.NextCommand = recoveryCLI() + " secrets check --app " + recoveryArgument(app) + " --scope " + recoveryArgument(scope)
			if file != "" {
				column.NextCommand += " --required-file " + recoveryArgument(file)
			}
			if file == "" || source == "explicit_file_and_keys" {
				for _, key := range required {
					column.NextCommand += " --required " + recoveryArgument(key)
				}
			}
		}
		report.Scopes = append(report.Scopes, column)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderSecretScopeComparison(report)
	}
	if ctx.Err() == context.Canceled {
		return 130
	}
	if ctx.Err() == context.DeadlineExceeded {
		return 124
	}
	if failed {
		return 1
	}
	return 0
}

func renderSecretScopeComparison(report secretScopeComparison) {
	_, _ = fmt.Fprintf(osStdout, "Required secret names: app=%s; source=%s\n", oneLine(report.App), report.RequirementsSource)
	if report.RequirementsFile != "" {
		_, _ = fmt.Fprintf(osStdout, "Declaration: %s\n", oneLine(report.RequirementsFile))
	}
	if report.DefaultsFile != "" {
		_, _ = fmt.Fprintf(osStdout, "Comparison defaults: %s\n", oneLine(report.DefaultsFile))
	}
	table := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprint(table, "KEY")
	for _, scope := range report.Scopes {
		_, _ = fmt.Fprintf(table, "\t%s", scope.Scope)
	}
	_, _ = fmt.Fprintln(table)
	for _, row := range report.Keys {
		_, _ = fmt.Fprint(table, row.Key)
		for _, scope := range report.Scopes {
			_, _ = fmt.Fprintf(table, "\t%s", row.States[scope.Scope])
		}
		_, _ = fmt.Fprintln(table)
	}
	_ = table.Flush()
	if len(report.Keys) == 0 {
		_, _ = fmt.Fprintln(osStdout, "The explicit declaration contains no required keys.")
	}
	for _, scope := range report.Scopes {
		if scope.Message != "" {
			_, _ = fmt.Fprintf(osStdout, "%s: %s\n", scope.Scope, scope.Message)
		}
		if scope.NextCommand != "" {
			_, _ = fmt.Fprintf(osStdout, "Check %s (POSIX shells):\n  %s\n", scope.Scope, scope.NextCommand)
		}
	}
	_, _ = fmt.Fprintln(osStdout, "Configured means a sealed key is present in scoped metadata; values, delivery, reference destinations, and registered environment existence are not verified.")
}
