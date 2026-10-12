package main

import "fmt"

// targetValueSource records provenance before a command replaces its flags
// with resolved context. Empty linked environments retain the default scope.
func targetValueSource(explicit, resolved string) string {
	if explicit != "" {
		return "explicit flag"
	}
	if resolved != "" {
		return "linked project"
	}
	return "default"
}

func printMutationTarget(app, scope, appSource, scopeSource string) {
	if jsonOutput {
		return
	}
	profileSource := "selected connection profile"
	if profileOverride != "" {
		profileSource = "explicit --profile"
	}
	_, _ = fmt.Fprintf(osStderr, "Target: profile=%s (%s); app=%s (%s); scope=%s (%s)\n",
		compactInspectFailureText(currentProfile()), profileSource,
		compactInspectFailureText(app), appSource,
		compactInspectFailureText(scopeOrDefault(scope)), scopeSource)
}
