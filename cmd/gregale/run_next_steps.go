package main

import "fmt"

// Human guidance stays outside JSON branches and uses the selected profile.
func printWorkflowRunNextSteps(id, status string) {
	switch status {
	case "pending":
		_, _ = fmt.Fprintln(osStdout, "The run is queued; execution has not started yet.")
	case "running":
		_, _ = fmt.Fprintln(osStdout, "The run is executing; it has not completed yet.")
	case "awaiting_event":
		_, _ = fmt.Fprintln(osStdout, "The run is waiting for an event.")
	}
	_, _ = fmt.Fprintf(osStdout, "\nInspect this run (POSIX shells):\n  %s workflows status %s\n", recoveryCLI(), recoveryArgument(id))
	if status == "pending" || status == "running" || status == "awaiting_event" {
		_, _ = fmt.Fprintf(osStdout, "\nWait for completion (POSIX shells):\n  %s workflows wait %s\n", recoveryCLI(), recoveryArgument(id))
	}
	if status == "failed" || status == "dead" {
		_, _ = fmt.Fprintf(osStdout, "\nDiagnose the failure before retrying:\n  %s workflows diagnose %s\n", recoveryCLI(), recoveryArgument(id))
	}
}

func printJobRunNextSteps(name, id string) {
	_, _ = fmt.Fprintln(osStdout, "Acceptance does not confirm that the tasks completed.")
	_, _ = fmt.Fprintf(osStdout, "\nFollow this run to completion (POSIX shells):\n  %s jobs wait %s %s\n", recoveryCLI(), recoveryArgument(name), recoveryArgument(id))
	_, _ = fmt.Fprintf(osStdout, "\nInspect task results:\n  %s jobs tasks %s %s\n", recoveryCLI(), recoveryArgument(name), recoveryArgument(id))
}
