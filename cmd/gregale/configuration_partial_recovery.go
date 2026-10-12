package main

import (
	"fmt"
	"strings"
)

// Keep existing JSON envelopes and exit classification. Report only confirmed
// steps; a failed response does not prove that the attempted mutation did not run.
func printConfigurationPartialError(title string, err error, app, scope, confirmed string, keys []string, wakeID string) int {
	code := printErr(title, err)
	if jsonOutput {
		return code
	}
	_, _ = fmt.Fprintf(osStderr, "\nConfirmed: %s\n  App: %s; scope: %s\n", compactInspectFailureText(confirmed), compactInspectFailureText(app), compactInspectFailureText(scopeOrDefault(scope)))
	if len(keys) > 0 {
		_, _ = fmt.Fprintf(osStderr, "  Confirmed keys: %s\n", compactInspectFailureText(strings.Join(keys, ", ")))
	}
	_, _ = fmt.Fprintln(osStderr, "The remaining step was not confirmed. Do not repeat the whole operation just to recover that step.")
	_, _ = fmt.Fprintf(osStderr, "Review saved secret metadata (POSIX shells):\n  %s secrets list --app %s --scope %s\n", recoveryCLI(), recoveryArgument(app), recoveryArgument(scopeOrDefault(scope)))
	command := recoveryCLI() + " inspect " + recoveryArgument(app)
	if wakeID != "" {
		command = recoveryCLI() + " app " + recoveryArgument(app) + " restart status --wake-id " + recoveryArgument(wakeID)
	}
	_, _ = fmt.Fprintf(osStderr, "Check current operations before requesting another restart:\n  %s\n", command)
	return code
}
