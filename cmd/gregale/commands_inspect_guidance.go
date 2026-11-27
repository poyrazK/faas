package main

import (
	"fmt"
	"io"
)

func renderInspectFailureDetails(w io.Writer, summary inspectSummary) {
	if summary.Failure == nil {
		return
	}
	failure := *summary.Failure
	if !failure.Candidate {
		_, reason, _ := inspectAttention(summary)
		if reason == "The latest deployment failed." && failure.Cause != "" {
			failure.Cause = "" // The overview already presents this cause.
		}
	}
	_, _ = fmt.Fprintln(w, "\nFailure details")
	renderInspectFailureOverview(w, &failure)
	if failure.CrashedSince != "" {
		// The deployment log ends with the crash verdict and never holds the
		// app's own output (prod hunt 2026-11-21).
		_, _ = fmt.Fprintf(w, "  What the app printed before it exited (POSIX shells):\n    %s logs %s --since %s\n", recoveryCLI(), recoveryArgument(summary.App.Slug), failure.CrashedSince)
	} else {
		printDeploymentLogCommand(w, summary.App.Slug, failure.DeploymentID)
	}
	_, _, primary := inspectAttention(summary)
	details := recoveryCLI() + " deployment get " + recoveryArgument(failure.DeploymentID)
	if details != primary {
		_, _ = fmt.Fprintf(w, "  Full details (POSIX shells):\n    %s\n", details)
	}
}

func renderInspectRemainingRecommendations(w io.Writer, summary inspectSummary) {
	_, reason, _ := inspectAttention(summary)
	primaryAdvice := ""
	remaining := make([]inspectRecommendation, 0, len(summary.Recommendations))
	seen := map[string]bool{}
	for _, recommendation := range summary.Recommendations {
		key := recommendation.Message + "\x00" + recommendation.Next
		if recommendation.Message == reason {
			if primaryAdvice == "" {
				primaryAdvice = recommendation.Next
			}
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		remaining = append(remaining, recommendation)
	}
	if primaryAdvice != "" {
		_, _ = fmt.Fprintf(w, "\nPrimary guidance:\n    next: %s\n", compactInspectFailureText(primaryAdvice))
	}
	if len(remaining) == 0 && len(summary.Recommendations) > 0 {
		return // The primary recommendation is already in the overview.
	}
	renderInspectRecommendations(w, remaining)
}
