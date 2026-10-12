package main

import "fmt"

func printAppReadContext(slug, environment string) {
	if jsonOutput {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "App settings: %s\n  Connection profile: %s\n", compactInspectFailureText(slug), compactInspectFailureText(currentProfile()))
	if environment == "" {
		_, _ = fmt.Fprintln(osStdout, "  Target: app-level settings (no named environment selected)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "  Target: environment=%s (explicit --environment)\n", compactInspectFailureText(environment))
		_, _ = fmt.Fprintln(osStdout, "These are the named environment's settings. They do not confirm a deployment or release there. URL and app status below are app metadata, not verification of this environment.")
	}
	_, _ = fmt.Fprintln(osStdout, "This reads saved settings and recorded app metadata; it does not run a deployment or health probe.")
	_, _ = fmt.Fprintln(osStdout)
}
