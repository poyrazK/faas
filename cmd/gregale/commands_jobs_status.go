package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsStatus(args []string) int {
	fs := newFlagSet("jobs status", flag.ContinueOnError)
	check := fs.Bool("check", false, "exit successfully only when the run has succeeded")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	args = fs.Args()
	if len(args) > 2 {
		return printErr("Invalid Job status target", fmt.Errorf("usage: gregale job status [JOB] [RUN_ID|latest] [--check]"))
	}
	if len(args) == 0 {
		defaults, path, err := readJobDefaultsForCommand([]string{"jobs", "status", "my-job"})
		if err != nil {
			return printJobDefaultsSelectionError("Could not read Job defaults", err)
		}
		args = []string{defaults.Job}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
		}
	}
	if len(args) == 1 {
		args = append(args, "latest")
	}
	if len(args) != 2 || !jobSlugPattern.MatchString(args[0]) || (args[1] != "latest" && !jobRunIDPattern.MatchString(args[1])) {
		return printErr("Invalid Job status target", fmt.Errorf("usage: gregale job status [JOB] [RUN_ID|latest] [--check]; provide a valid Job name and run UUID or latest"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	job, err := client.GetJob(ctx, args[0])
	if err != nil {
		return printErr("Could not read Job", err)
	}
	if job.Name != args[0] || job.ID == "" || job.AccountID == "" {
		return printErr("Invalid Job", fmt.Errorf("server did not identify the requested Job"))
	}
	id, err := resolveJobRunReadArg(ctx, client, args[0], args[1])
	if err != nil {
		return printErr("Could not resolve Job run", err)
	}
	run, err := client.GetJobRun(ctx, args[0], id)
	if err != nil {
		return printErr("Could not read Job run", err)
	}
	if run.ID != id || run.JobID != job.ID || run.AccountID != job.AccountID {
		return printErr("Invalid Job run", fmt.Errorf("server returned another run, Job, or account"))
	}
	checkResult := func() int {
		if *check && run.AggregateStatus != "succeeded" {
			return printErr("Job run check failed", fmt.Errorf("run %s has recorded status %s; only succeeded passes --check", run.ID, oneLine(run.AggregateStatus)))
		}
		return 0
	}
	if jsonOutput {
		if code := jsonOut(writeJSONSingle(run)); code != 0 {
			return code
		}
		return checkResult()
	}
	_, _ = fmt.Fprintf(osStdout, "Job:          %s\nRun ID:       %s\nStatus:       %s\nTrigger:      %s\nTasks:        %d total; %d running; %d succeeded; %d failed; %d cancelled\nDead-letter:  %d\nParallelism:  %d\n", args[0], run.ID, oneLine(run.AggregateStatus), oneLine(run.TriggerKind), run.Tasks, run.TasksRunning, run.TasksSucceeded, run.TasksFailed, run.TasksCancelled, run.DeadLetterCount, run.Parallelism)
	for _, field := range []struct{ label, value string }{{"Created:", run.CreatedAt}, {"Started:", run.StartedAt}, {"Finished:", run.FinishedAt}, {"Source run:", run.SourceRunID}} {
		if field.value != "" {
			_, _ = fmt.Fprintf(osStdout, "%-13s %s\n", field.label, oneLine(field.value))
		}
	}
	printJobHistoryNextSteps(osStdout, args[0], []api.JobRunResponse{run})
	_, _ = fmt.Fprintf(osStdout, "\nInspect all task outcomes (POSIX shells):\n  %s jobs tasks %s %s --all\n", recoveryCLI(), recoveryArgument(args[0]), recoveryArgument(run.ID))
	return checkResult()
}
