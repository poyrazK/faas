package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Resolve once before mutation, then validate the selected run's ownership.
func resolveJobRetryRun(ctx context.Context, client *api.Client, name, ref string) (string, error) {
	return resolveJobRecoveryRun(ctx, client, name, ref, "Retry target", "retry")
}

func resolveJobRecoveryRun(ctx context.Context, client *api.Client, name, ref, label, action string) (string, error) {
	job, err := client.GetJob(ctx, name)
	if err != nil {
		return "", err
	}
	if job.Name != name || !jobRunIDPattern.MatchString(job.ID) || job.AccountID == "" {
		return "", fmt.Errorf("job response did not identify the requested Job")
	}
	if ref == "latest" {
		page, err := client.ListJobRunsPage(ctx, name, 1, 0)
		if err != nil {
			return "", err
		}
		if len(page.Runs) == 0 {
			return "", fmt.Errorf("no runs found for Job %q; inspect %s jobs info %s", name, recoveryCLI(), recoveryArgument(name))
		}
		ref = page.Runs[0].ID
		if !jobRunIDPattern.MatchString(ref) {
			return "", fmt.Errorf("newest Job run response has an invalid run ID")
		}
	}
	run, err := client.GetJobRun(ctx, name, ref)
	if err != nil {
		return "", err
	}
	if run.ID != ref || run.JobID != job.ID || run.AccountID != job.AccountID {
		return "", fmt.Errorf("selected run did not match the requested Job")
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "%s: job=%s; run=%s. Newest means the first run in Job history; %s eligibility is checked by the server.\n", label, name, ref, action)
	}
	return ref, nil
}
