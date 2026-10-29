package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
)

type runArtifactItem struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Status    string `json:"status"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
	metadata  jobresult.Artifact
}

type runArtifactTask struct {
	TaskIndex      int               `json:"task_index"`
	Attempt        int               `json:"attempt"`
	TaskStatus     string            `json:"task_status"`
	ManifestStatus string            `json:"manifest_status"`
	Artifacts      []runArtifactItem `json:"artifacts"`
}

type jobRunArtifactReceipt struct {
	OutputDirectory     string            `json:"output_directory"`
	Include             []string          `json:"include,omitempty"`
	Exclude             []string          `json:"exclude,omitempty"`
	DirectoriesToCreate []string          `json:"directories_to_create,omitempty"`
	Job                 string            `json:"job"`
	RunID               string            `json:"run_id"`
	DryRun              bool              `json:"dry_run"`
	Ready               bool              `json:"ready"`
	Tasks               []runArtifactTask `json:"tasks"`
	Summary             map[string]int    `json:"summary"`
	DownloadBytes       json.Number       `json:"download_bytes"`
	DownloadCommand     string            `json:"download_command,omitempty"`
	ResumeCommand       string            `json:"resume_command,omitempty"`
}

// Only open real task directories, keeping reads and writes inside the pinned parent.
func openArtifactTaskDirectory(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("task destination must be a real directory, not a file or symlink")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("cannot open task destination directory")
	}
	opened, err := root.Stat(".")
	after, afterErr := parent.Lstat(name)
	if err != nil || afterErr != nil || !after.IsDir() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = root.Close()
		return nil, fmt.Errorf("task destination directory changed during inspection")
	}
	return root, nil
}

func downloadJobRunArtifactsWithFilters(pos []string, directory string, resume, dryRun bool, includes, excludes []string, accepted *api.JobRunResponse, receive func(jobRunArtifactReceipt) int) int {
	return downloadJobRunArtifactsWithContext(context.Background(), pos, directory, resume, dryRun, includes, excludes, accepted, receive)
}

func downloadJobRunArtifactsWithContext(parentCtx context.Context, pos []string, directory string, resume, dryRun bool, includes, excludes []string, accepted *api.JobRunResponse, receive func(jobRunArtifactReceipt) int) int {
	if len(pos) != 2 || directory == "" || !jobSlugPattern.MatchString(pos[0]) || (pos[1] != "latest" && !jobRunIDPattern.MatchString(pos[1])) {
		return printErr("Invalid whole-run download", fmt.Errorf("usage: gregale job artifact download JOB RUN_ID|latest --all-tasks --out-dir DIR [--dry-run] [--resume]"))
	}
	if resume && pos[1] == "latest" {
		return printErr("Invalid resume target", fmt.Errorf("--resume requires the explicit run UUID from the previous receipt"))
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return printErr("Invalid output directory", err)
	}
	outputDirectory, err := inspectArtifactOutputDirectory(directory)
	if err != nil {
		return printErr("Could not inspect output directory", err)
	}
	defer outputDirectory.Close()
	parent := outputDirectory.root
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 5*time.Minute)
	defer cancel()
	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()
	job, err := client.GetJob(readCtx, pos[0])
	if err != nil {
		return printErr("Could not read Job", err)
	}
	if job.Name != pos[0] || job.ID == "" || job.AccountID == "" {
		return printErr("Invalid Job", fmt.Errorf("server did not identify the requested Job"))
	}
	if accepted != nil && (job.ID != accepted.JobID || job.AccountID != accepted.AccountID || pos[1] != accepted.ID) {
		return printErr("Accepted run identity changed", fmt.Errorf("artifact collection target no longer matches the accepted run, Job, or account"))
	}
	id, err := resolveJobRunReadArg(readCtx, client, pos[0], pos[1])
	if err != nil {
		return printErr("Could not resolve Job run", err)
	}
	run, err := client.GetJobRun(readCtx, pos[0], id)
	if err != nil {
		return printErr("Could not read Job run", err)
	}
	if run.ID != id || run.JobID != job.ID || run.AccountID != job.AccountID {
		return printErr("Invalid Job run", fmt.Errorf("server returned another run, Job, or account"))
	}
	tasks, err := collectJobTaskPagesContext(ctx, client, pos[0], id, 200, 0, false)
	if err != nil {
		return printErr("Could not discover task artifacts", err)
	}
	plans := make([]runArtifactTask, 0, len(tasks))
	// Validate every manifest before inspecting destinations or emitting any results.
	for _, task := range tasks {
		plan := runArtifactTask{TaskIndex: task.TaskIndex, Attempt: task.Attempt, TaskStatus: task.Status, ManifestStatus: "missing", Artifacts: make([]runArtifactItem, 0)}
		if len(task.OutputManifest) > 0 && strings.TrimSpace(string(task.OutputManifest)) != "null" {
			manifest, err := jobresult.Validate(task.OutputManifest)
			if err != nil {
				return printErr("Invalid output manifest", fmt.Errorf("task %d has an invalid recorded manifest; no files were downloaded", task.TaskIndex))
			}
			plan.ManifestStatus = "recorded"
			if len(manifest.Artifacts) == 0 {
				plan.ManifestStatus = "empty"
			}
			for _, artifact := range manifest.Artifacts {
				plan.Artifacts = append(plan.Artifacts, runArtifactItem{Name: artifact.Name, SizeBytes: artifact.SizeBytes, metadata: artifact})
			}
		}
		plans = append(plans, plan)
	}
	conflicts := false
	plannedDirectories := make([]string, 0)
	for t := range plans {
		plan := &plans[t]
		dirname := fmt.Sprintf("task-%d", plan.TaskIndex)
		managed := false
		for _, item := range plan.Artifacts {
			if jobArtifactSelected(item.Name, includes, excludes) && strings.HasPrefix(item.metadata.URI, "obj://") {
				managed = true
			}
		}
		var root *os.Root
		var directoryErr error
		if managed {
			if parent == nil {
				if len(plannedDirectories) == 0 {
					plannedDirectories = append(plannedDirectories, outputDirectory.planned...)
				}
				plannedDirectories = append(plannedDirectories, filepath.Join(directory, dirname))
			} else {
				_, err := parent.Lstat(dirname)
				if os.IsNotExist(err) {
					plannedDirectories = append(plannedDirectories, filepath.Join(directory, dirname))
				} else {
					root, directoryErr = openArtifactTaskDirectory(parent, dirname)
				}
			}
		}
		for a := range plan.Artifacts {
			if ctx.Err() != nil {
				if root != nil {
					_ = root.Close()
				}
				return printErr("Artifact discovery interrupted", ctx.Err())
			}
			item := &plan.Artifacts[a]
			if !jobArtifactSelected(item.Name, includes, excludes) {
				item.Status = "skipped_filter"
				continue
			}
			if !strings.HasPrefix(item.metadata.URI, "obj://") {
				item.Status = "skipped_external"
				continue
			}
			if item.Name == "." || item.Name == ".." || filepath.Base(item.Name) != item.Name || item.SizeBytes == math.MaxInt64 {
				item.Status, item.Error = "conflict", "artifact name or size cannot be used for a local download"
			} else {
				item.Path = filepath.Join(directory, dirname, item.Name)
				if directoryErr != nil {
					item.Status, item.Error = "conflict", directoryErr.Error()
				} else if root == nil {
					item.Status = "would_download"
				} else {
					info, err := root.Lstat(item.Name)
					if os.IsNotExist(err) {
						item.Status = "would_download"
					} else if err != nil {
						item.Status, item.Error = "conflict", "cannot inspect destination"
					} else if !resume {
						item.Status, item.Error = "conflict", "destination exists; use --resume to verify it; no overwrite"
					} else if !info.Mode().IsRegular() {
						item.Status, item.Error = "conflict", "destination must be a regular file, not a symlink or directory"
					} else if err := verifyExistingJobArtifact(ctx, root, item.metadata, info); err != nil {
						item.Status, item.Error = "conflict", "existing file cannot be verified against the discovered size and SHA-256"
					} else {
						item.Status = "verified_existing"
					}
				}
			}
			if item.Status == "conflict" {
				conflicts = true
			}
		}
		if root != nil {
			_ = root.Close()
		}
	}
	if ctx.Err() != nil {
		return printErr("Artifact discovery interrupted", ctx.Err())
	}
	plannedBytes := new(big.Int)
	for _, plan := range plans {
		for _, item := range plan.Artifacts {
			if item.Status == "would_download" {
				plannedBytes.Add(plannedBytes, big.NewInt(item.SizeBytes))
			}
		}
	}
	// Preflight the entire run before creating directories or requesting signed URLs.
	if !dryRun && !conflicts {
		needsDirectory := false
		for _, plan := range plans {
			for _, item := range plan.Artifacts {
				if item.Status == "would_download" || item.Status == "verified_existing" {
					needsDirectory = true
				}
			}
		}
		if needsDirectory && parent == nil {
			if err := outputDirectory.create(ctx); err != nil {
				return printErr("Could not create output directory", err)
			}
			parent = outputDirectory.root
		}
		for t := range plans {
			plan := &plans[t]
			dirname := fmt.Sprintf("task-%d", plan.TaskIndex)
			var root *os.Root
			var directoryErr error
			for a := range plan.Artifacts {
				item := &plan.Artifacts[a]
				if item.Status == "skipped_external" || item.Status == "skipped_filter" {
					continue
				}
				if ctx.Err() != nil {
					item.Status, item.Error = "not_attempted", "download interrupted or deadline reached"
					continue
				}
				if root == nil && directoryErr == nil {
					if _, err := parent.Lstat(dirname); os.IsNotExist(err) {
						// Exclusive creation; a raced destination causes failure rather than being reused.
						if err := parent.Mkdir(dirname, 0750); err != nil {
							directoryErr = fmt.Errorf("could not create new task directory")
						}
					}
					if directoryErr == nil {
						root, directoryErr = openArtifactTaskDirectory(parent, dirname)
					}
				}
				if directoryErr != nil {
					item.Status, item.Error = "failed", directoryErr.Error()
					continue
				}
				if item.Status == "verified_existing" {
					info, err := root.Lstat(item.Name)
					if err == nil {
						err = verifyExistingJobArtifact(ctx, root, item.metadata, info)
					}
					if err != nil {
						item.Status, item.Error = "failed", "existing file changed or cannot be verified; not overwritten"
					}
					continue
				}
				fetchCtx, fetchCancel := context.WithTimeout(ctx, 30*time.Second)
				receipt, fetchErr := client.DownloadJobArtifact(fetchCtx, pos[0], id, plan.TaskIndex, item.Name)
				fetchCancel()
				if fetchErr != nil {
					item.Status, item.Error = "failed", "could not request artifact; inspect the current task and retry"
				} else if receipt.Name != item.Name || receipt.SizeBytes != item.SizeBytes || strings.TrimPrefix(receipt.SHA256, "sha256:") != strings.TrimPrefix(item.metadata.SHA256, "sha256:") {
					item.Status, item.Error = "failed", "artifact metadata changed since discovery; no file saved"
				} else if err := saveDownloadedJobArtifact(ctx, client, root, item.Name, item.Name, receipt); err != nil {
					item.Status, item.Error = "failed", err.Error()
				} else {
					item.Status = "saved"
				}
			}
			if root != nil {
				_ = root.Close()
			}
		}
	} else if !dryRun {
		for t := range plans {
			for a := range plans[t].Artifacts {
				item := &plans[t].Artifacts[a]
				if item.Status == "would_download" {
					item.Status = "not_attempted"
				}
			}
		}
	}
	counts := map[string]int{"saved": 0, "verified_existing": 0, "failed": 0, "skipped_external": 0, "not_attempted": 0, "would_download": 0, "conflict": 0}
	if len(includes) > 0 || len(excludes) > 0 {
		counts["skipped_filter"] = 0
	}
	incomplete := conflicts
	for _, plan := range plans {
		for _, item := range plan.Artifacts {
			counts[item.Status]++
			if item.Status == "failed" || item.Status == "not_attempted" {
				incomplete = true
			}
		}
	}
	command := fmt.Sprintf("%s jobs artifact-download %s %s --all-tasks --out-dir %s", recoveryCLI(), recoveryArgument(pos[0]), recoveryArgument(id), recoveryArgument(directory))
	if resume {
		command += " --resume"
	}
	resumeCommand := ""
	if !dryRun && (incomplete || ctx.Err() != nil) {
		resumeCommand = fmt.Sprintf("%s jobs artifact-download %s %s --all-tasks --out-dir %s --resume", recoveryCLI(), recoveryArgument(pos[0]), recoveryArgument(id), recoveryArgument(directory))
	}
	command = appendJobArtifactFilters(command, includes, excludes)
	if resumeCommand != "" {
		resumeCommand = appendJobArtifactFilters(resumeCommand, includes, excludes)
	}
	previewCommand := ""
	if dryRun {
		previewCommand = command
	}
	receipt := jobRunArtifactReceipt{directory, includes, excludes, plannedDirectories, pos[0], id, dryRun, !incomplete && ctx.Err() == nil, plans, counts, json.Number(plannedBytes.String()), previewCommand, resumeCommand}
	if receive != nil {
		if code := receive(receipt); code != 0 {
			return code
		}
	} else if jsonOutput {
		if code := jsonOut(writeJSONSingle(receipt)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Whole-run artifacts: Job %s; run %s; dry-run=%t\n", pos[0], id, dryRun)
		if dryRun {
			for _, path := range plannedDirectories {
				_, _ = fmt.Fprintf(osStdout, "Would create directory: %s\n", oneLine(path))
			}
		}
		for _, plan := range plans {
			_, _ = fmt.Fprintf(osStdout, "Task %d; attempt %d; status %s; manifest %s\n", plan.TaskIndex, plan.Attempt, oneLine(plan.TaskStatus), plan.ManifestStatus)
			for _, item := range plan.Artifacts {
				_, _ = fmt.Fprintf(osStdout, "  %s: %s; %d bytes", oneLine(item.Name), item.Status, item.SizeBytes)
				if item.Path != "" {
					_, _ = fmt.Fprintf(osStdout, "; %s", oneLine(item.Path))
				}
				if item.Error != "" {
					_, _ = fmt.Fprintf(osStdout, "; %s", oneLine(item.Error))
				}
				_, _ = fmt.Fprintln(osStdout)
			}
		}
		_, _ = fmt.Fprintf(osStdout, "Summary: %d saved; %d verified existing; %d failed; %d skipped external; %d not attempted; %d conflicts; %d would download; planned transfer bytes=%s.\n", counts["saved"], counts["verified_existing"], counts["failed"], counts["skipped_external"], counts["not_attempted"], counts["conflict"], counts["would_download"], plannedBytes.String())
		if len(includes) > 0 || len(excludes) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Filtered artifacts skipped: %d.\n", counts["skipped_filter"])
		}
		if previewCommand != "" {
			_, _ = fmt.Fprintf(osStdout, "Download after addressing conflicts (POSIX shells):\n  %s\n", previewCommand)
		}
		if resumeCommand != "" {
			_, _ = fmt.Fprintf(osStdout, "Resume after addressing failures (POSIX shells):\n  %s\n", resumeCommand)
		}
		_, _ = fmt.Fprintln(osStdout, "Current task manifests only; external artifacts require their storage provider. Downloads verify bytes and never overwrite files. Previously saved files and task directories remain after failures; active tasks may produce outputs later.")
		if dryRun {
			_, _ = fmt.Fprintln(osStdout, "Preview only: no signed URLs requested, directories created, or files written. Local files and task outputs may change before download.")
		}
	}
	if ctx.Err() != nil {
		return printErr("Whole-run artifact operation interrupted", ctx.Err())
	}
	if incomplete {
		return printErr("Whole-run artifact operation incomplete", fmt.Errorf("address the reported conflicts or failures; existing files are never overwritten"))
	}
	return 0
}
