package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/jobresult"
)

type jobArtifactListing struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Managed   bool   `json:"managed"`
}

func cmdJobsArtifacts(args []string) int {
	remaining, taskFlag, taskSet, err := splitJobLogTask(args)
	if err != nil {
		return printErr("Invalid task selection", err)
	}
	args = remaining
	fs := newFlagSet("jobs artifacts", flag.ContinueOnError)
	includes := registerJobsMultiFlag(fs, "include", "repeatable artifact-name glob; quote patterns")
	excludes := registerJobsMultiFlag(fs, "exclude", "repeatable artifact-name glob to omit; exclusions take precedence")
	attempt := fs.Int("attempt", 0, "inspect a positive retained attempt number (metadata only)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	patterns := []string(*includes)
	exclusions := []string(*excludes)
	if err := validateJobArtifactExcludes(exclusions); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if err := validateJobArtifactIncludes(patterns); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	specified := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "attempt" {
			specified = true
		}
	})
	if specified && *attempt < 1 {
		return printErr("Invalid attempt", fmt.Errorf("--attempt requires a positive attempt number"))
	}
	args = fs.Args()
	if (taskSet && len(args) > 2) || (!taskSet && len(args) != 3) {
		return printErr("Invalid artifact listing target", fmt.Errorf("use jobs artifacts [JOB] [RUN_ID|latest] --task N, or jobs artifacts JOB RUN_ID|latest TASK_INDEX"))
	}
	if taskSet {
		if len(args) == 0 {
			defaults, path, err := readJobDefaults()
			if err != nil {
				return printErr("Could not read Job defaults", err)
			}
			args = []string{defaults.Job}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
			}
		}
		if len(args) == 1 {
			args = append(args, "latest")
		}
		args = append(args, taskFlag)
	}
	if !jobSlugPattern.MatchString(args[0]) || (args[1] != "latest" && !jobRunIDPattern.MatchString(args[1])) {
		return printErr("Invalid artifact listing target", fmt.Errorf("usage: gregale job artifacts JOB RUN_ID|latest TASK_INDEX [--attempt N] [--include GLOB ...]"))
	}
	index, err := strconv.Atoi(args[2])
	if err != nil || index < 0 {
		return printErr("Invalid task index", fmt.Errorf("provide a non-negative task index"))
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
	var task api.JobTaskResponse
	if specified {
		run, err := client.GetJobRun(ctx, args[0], id)
		if err != nil {
			return printErr("Could not read Job run", err)
		}
		if run.ID != id || run.JobID != job.ID || run.AccountID != job.AccountID {
			return printErr("Invalid Job run", fmt.Errorf("server returned another run, Job, or account"))
		}
		retained, err := findRetainedJobArtifactAttempt(ctx, client, args[0], id, index, *attempt)
		if err != nil {
			return printErr("Could not read retained attempt", err)
		}
		task = api.JobTaskResponse{RunID: id, TaskIndex: index, Attempt: retained.Attempt, Status: retained.Status, OutputManifest: retained.OutputManifest}
	} else {
		_, current, err := readJobRetryTarget(ctx, client, job, id, index)
		if err != nil {
			return printErr("Could not read selected task", err)
		}
		task = current
	}
	hasManifest := len(task.OutputManifest) > 0 && strings.TrimSpace(string(task.OutputManifest)) != "null"
	recordedArtifacts := 0
	artifacts := make([]jobArtifactListing, 0)
	if hasManifest {
		manifest, err := jobresult.Validate(task.OutputManifest)
		if err != nil {
			return printErr("Invalid output manifest", fmt.Errorf("the selected task's recorded output manifest is invalid; no artifact guidance can be confirmed"))
		}
		recordedArtifacts = len(manifest.Artifacts)
		for _, artifact := range manifest.Artifacts {
			if !jobArtifactSelected(artifact.Name, patterns, exclusions) {
				continue
			}
			artifacts = append(artifacts, jobArtifactListing{artifact.Name, artifact.SizeBytes, artifact.SHA256, strings.HasPrefix(artifact.URI, "obj://")})
		}
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(struct {
			Include     []string             `json:"include,omitempty"`
			Exclude     []string             `json:"exclude,omitempty"`
			RunID       string               `json:"run_id"`
			TaskIndex   int                  `json:"task_index"`
			Attempt     int                  `json:"attempt"`
			TaskStatus  string               `json:"task_status"`
			HasManifest bool                 `json:"has_manifest"`
			Artifacts   []jobArtifactListing `json:"artifacts"`
		}{patterns, exclusions, id, index, task.Attempt, task.Status, hasManifest, artifacts}))
	}
	_, _ = fmt.Fprintf(osStdout, "Job: %s; run: %s; task: %d; attempt: %d; status: %s\n", args[0], id, index, task.Attempt, oneLine(task.Status))
	if len(patterns) > 0 || len(exclusions) > 0 {
		_, _ = fmt.Fprintln(osStdout, "Showing recorded artifacts matching the "+jobArtifactFilterDescription(exclusions)+".")
	}
	if specified {
		_, _ = fmt.Fprintln(osStdout, "Retained attempt metadata only. Historical artifact downloads are not supported; artifact-download reads the current task output.")
	}
	if !hasManifest {
		_, _ = fmt.Fprintln(osStdout, "No output manifest recorded for this task. It may still be active or have produced no retained result.")
		return 0
	}
	if len(artifacts) == 0 && recordedArtifacts > 0 && (len(patterns) > 0 || len(exclusions) > 0) {
		_, _ = fmt.Fprintln(osStdout, "No recorded artifact names match the "+jobArtifactFilterDescription(exclusions)+".")
		return 0
	}
	if len(artifacts) == 0 {
		_, _ = fmt.Fprintln(osStdout, "The recorded output manifest contains no artifacts.")
		return 0
	}
	for _, artifact := range artifacts {
		storage := "external storage"
		if artifact.Managed {
			storage = "Gregale managed"
		}
		_, _ = fmt.Fprintf(osStdout, "\n%s: %d bytes; %s; %s\n", oneLine(artifact.Name), artifact.SizeBytes, artifact.SHA256, storage)
		if specified {
			_, _ = fmt.Fprintln(osStdout, "  Recorded artifact metadata; no historical download command is available.")
		} else if artifact.Managed {
			_, _ = fmt.Fprintf(osStdout, "  Download to a new file (POSIX shells):\n    %s jobs artifact-download --out %s -- %s %s %d %s\n", recoveryCLI(), recoveryArgument("./"+artifact.Name), recoveryArgument(args[0]), recoveryArgument(id), index, recoveryArgument(artifact.Name))
		} else {
			_, _ = fmt.Fprintln(osStdout, "  Access this artifact through its storage provider; Gregale download commands apply to managed artifacts.")
		}
	}
	if specified {
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "\nRecorded manifest metadata; download verifies the current managed artifact's size and SHA-256. Task outputs may change after a retry.")
	return 0
}

func findRetainedJobArtifactAttempt(ctx context.Context, client *api.Client, name, runID string, taskIndex, attempt int) (api.JobTaskAttemptResponse, error) {
	offset := 0
	seen := map[int]bool{}
	for pageNumber := 0; pageNumber < maxCLIListPages; pageNumber++ {
		page, err := client.ListJobTaskAttemptsPage(ctx, name, runID, taskIndex, 100, offset)
		if err != nil {
			return api.JobTaskAttemptResponse{}, err
		}
		var selected *api.JobTaskAttemptResponse
		for i := range page.Attempts {
			row := &page.Attempts[i]
			if row.RunID != runID || row.TaskIndex != taskIndex || row.Attempt < 1 || seen[row.Attempt] {
				return api.JobTaskAttemptResponse{}, fmt.Errorf("history returned another task, an invalid attempt, or a repeated attempt")
			}
			seen[row.Attempt] = true
			if row.Attempt == attempt {
				selected = row
			}
		}
		if selected != nil {
			return *selected, nil
		}
		if page.NextOffset == -1 || (page.NextOffset == 0 && offset == 0) {
			return api.JobTaskAttemptResponse{}, fmt.Errorf("attempt %d was not found in retained history; inspect %s jobs attempts %s %s %d --all; it may be active or no longer retained", attempt, recoveryCLI(), recoveryArgument(name), recoveryArgument(runID), taskIndex)
		}
		if len(page.Attempts) == 0 || page.NextOffset <= offset {
			return api.JobTaskAttemptResponse{}, fmt.Errorf("attempt pagination did not advance")
		}
		offset = page.NextOffset
	}
	return api.JobTaskAttemptResponse{}, fmt.Errorf("attempt history exceeded %d pages", maxCLIListPages)
}
