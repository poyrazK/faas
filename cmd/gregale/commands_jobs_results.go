package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Results composes the existing wait/collection and read-only preview flows.
func cmdJobsResults(args []string) int {
	fs := newFlagSet("jobs results", flag.ContinueOnError)
	directory := fs.String("out-dir", "", "destination (default ./results/JOB/RUN_ID)")
	dryRun := fs.Bool("dry-run", false, "preview current artifacts without waiting or writing files")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum wait duration")
	interval := fs.Duration("poll-interval", 2*time.Second, "polling interval")
	allArtifacts := fs.Bool("all-artifacts", false, "ignore saved artifact filters for this invocation")
	includes := registerJobsMultiFlag(fs, "include", "repeatable artifact-name glob; quote patterns")
	excludes := registerJobsMultiFlag(fs, "exclude", "repeatable artifact-name glob to omit; exclusions take precedence")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	directorySet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "out-dir" {
			directorySet = true
		}
	})
	ref := "latest"
	if fs.NArg() == 2 {
		ref = fs.Arg(1)
	}
	if err := validateAllJobArtifacts(fs, *allArtifacts); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	name, defaultsErr := applyJobResultsDefaults(fs, fs.Arg(0), includes, excludes, *allArtifacts)
	if defaultsErr != nil {
		return printJobDefaultsSelectionError("Could not apply Job defaults", defaultsErr)
	}
	if fs.NArg() > 2 || !jobSlugPattern.MatchString(name) || (ref != "latest" && !jobRunIDPattern.MatchString(ref)) || (directorySet && *directory == "") || *timeout <= 0 || *interval <= 0 {
		return printErr("Invalid results arguments", errors.New("use jobs results JOB [RUN_ID|latest] [--out-dir DIR], with positive --timeout and --poll-interval"))
	}
	if err := validateJobArtifactIncludes([]string(*includes)); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if err := validateJobArtifactExcludes([]string(*excludes)); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	// Inspect explicit destinations before any API request.
	absolute := ""
	if directorySet {
		var err error
		absolute, err = filepath.Abs(*directory)
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected, err := inspectArtifactOutputDirectory(absolute)
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected.Close()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
	defer cancel()
	job, err := client.GetJob(ctx, name)
	if err != nil {
		return printErr("Could not read Job", err)
	}
	if job.Name != name || job.ID == "" || job.AccountID == "" {
		return printErr("Invalid Job", errors.New("server did not identify the requested Job and account"))
	}
	id, err := resolveJobRunReadArg(ctx, client, job.Name, ref)
	if err != nil {
		return printErr("Could not resolve Job run", err)
	}
	cancel()
	if !directorySet {
		absolute, err = filepath.Abs(filepath.Join("results", job.Name, id))
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected, err := inspectArtifactOutputDirectory(absolute)
		if err != nil {
			return printErr("Invalid output directory", err)
		}
		inspected.Close()
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Results destination: %s\n", absolute)
	}
	selected := api.JobRunResponse{ID: id, JobID: job.ID, AccountID: job.AccountID}
	if !*dryRun {
		return waitAndCollectJobRun(signalCtx, client, job.Name, selected, *timeout, *interval, absolute, true, []string(*includes), []string(*excludes))
	}
	return downloadJobRunArtifactsWithFilters([]string{job.Name, id}, absolute, true, true, []string(*includes), []string(*excludes), &selected, nil)
}
