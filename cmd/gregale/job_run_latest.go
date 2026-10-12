package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// This selector belongs only to read commands; mutation targets remain explicit.
func resolveJobRunReadArg(ctx context.Context, client *api.Client, job, ref string) (string, error) {
	if ref != "latest" {
		return ref, nil
	}
	page, err := client.ListJobRunsPage(ctx, job, 1, 0)
	if err != nil {
		return "", fmt.Errorf("read newest run for job %s: %w", job, err)
	}
	if len(page.Runs) == 0 {
		return "", fmt.Errorf("no runs found for job %q; review its configuration with %s jobs info %s before submitting a run", job, recoveryCLI(), recoveryArgument(job))
	}
	run := page.Runs[0]
	if !jobRunIDPattern.MatchString(run.ID) {
		return "", fmt.Errorf("newest job run response has an invalid run ID")
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Selected latest run: job=%s; run=%s; status=%s.\n", compactInspectFailureText(job), run.ID, compactInspectFailureText(run.AggregateStatus))
		_, _ = fmt.Fprintln(osStderr, "Latest means newest in this job's run history, including pending or failed runs. This read uses the selected ID.")
	}
	return run.ID, nil
}
