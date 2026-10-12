package main

import "fmt"

func printEmptyResult(context, detail, next string) {
	_, _ = fmt.Fprintf(osStdout, "No results returned for %s.\n", compactInspectFailureText(context))
	if detail != "" {
		_, _ = fmt.Fprintf(osStdout, "  Checked: %s\n", compactInspectFailureText(detail))
	}
	_, _ = fmt.Fprintf(osStdout, "  Connection profile: %s\nNext step (POSIX shells):\n  %s\n", oneLine(currentProfile()), next)
}
