package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/jobresult"
)

type bulkJobArtifactResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
}

type bulkJobArtifactSummary struct {
	Saved            int `json:"saved"`
	VerifiedExisting int `json:"verified_existing"`
	Failed           int `json:"failed"`
	SkippedExternal  int `json:"skipped_external"`
	NotAttempted     int `json:"not_attempted"`
	SkippedFilter    int `json:"skipped_filter,omitempty"`
}

func downloadAllJobArtifacts(pos []string, directory string, resume, dryRun bool, includes, excludes []string) int {
	if len(pos) != 3 || directory == "" || !jobSlugPattern.MatchString(pos[0]) || (pos[1] != "latest" && !jobRunIDPattern.MatchString(pos[1])) {
		return printErr("Invalid bulk download", fmt.Errorf("usage: gregale job artifact download JOB RUN_ID|latest TASK_INDEX --all --out-dir DIR"))
	}
	if resume && pos[1] == "latest" {
		return printErr("Invalid resume target", fmt.Errorf("--resume requires the explicit run UUID from the previous receipt so recovery stays on the same run"))
	}
	index, err := strconv.Atoi(pos[2])
	if err != nil || index < 0 {
		return printErr("Invalid task index", fmt.Errorf("provide a non-negative task index"))
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return printErr("Invalid output directory", err)
	}
	outputDirectory, err := inspectArtifactOutputDirectory(directory)
	if err != nil {
		return printErr("Could not inspect output directory", err)
	}
	defer outputDirectory.Close()
	root := outputDirectory.root
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
	id, err := resolveJobRunReadArg(readCtx, client, pos[0], pos[1])
	if err != nil {
		return printErr("Could not resolve Job run", err)
	}
	_, task, err := readJobRetryTarget(readCtx, client, job, id, index)
	if err != nil {
		return printErr("Could not read selected task", err)
	}
	manifest, err := jobresult.Validate(task.OutputManifest)
	if err != nil {
		return printErr("No valid output manifest", fmt.Errorf("the current task has no valid recorded manifest; no files were downloaded"))
	}
	if dryRun {
		directories := outputDirectory.planned
		managed := false
		for _, artifact := range manifest.Artifacts {
			if jobArtifactSelected(artifact.Name, includes, excludes) && strings.HasPrefix(artifact.URI, "obj://") {
				managed = true
			}
		}
		if !managed {
			directories = nil
		}
		return previewBulkJobArtifacts(ctx, root, pos[0], id, index, task.Attempt, directory, manifest, resume, directories, includes, excludes)
	}
	verified := map[string]bool{}
	// Check every managed destination before requesting any signed URLs or writing files.
	for _, artifact := range manifest.Artifacts {
		if !jobArtifactSelected(artifact.Name, includes, excludes) || !strings.HasPrefix(artifact.URI, "obj://") {
			continue
		}
		if artifact.Name == "." || artifact.Name == ".." || filepath.Base(artifact.Name) != artifact.Name || artifact.SizeBytes == math.MaxInt64 {
			return printErr("Invalid artifact destination", fmt.Errorf("managed artifact name or size cannot be used for a local download"))
		}
		if root == nil {
			continue
		}
		info, err := root.Lstat(artifact.Name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return printErr("Cannot inspect artifact destination", err)
		}
		if !resume {
			return printErr("Cannot save artifacts", fmt.Errorf("all managed destinations must be new files; existing files and symlinks are not overwritten; no files were downloaded"))
		}
		if !info.Mode().IsRegular() {
			return printErr("Cannot resume artifacts", fmt.Errorf("existing destinations must be regular files, not symlinks or directories; no files were downloaded"))
		}
		if err := verifyExistingJobArtifact(ctx, root, artifact, info); err != nil {
			return printErr("Cannot resume artifacts", err)
		}
		verified[artifact.Name] = true
	}
	if ctx.Err() != nil {
		return printErr("Bulk download interrupted", ctx.Err())
	}
	needsDirectory := false
	for _, artifact := range manifest.Artifacts {
		if jobArtifactSelected(artifact.Name, includes, excludes) && strings.HasPrefix(artifact.URI, "obj://") {
			needsDirectory = true
		}
	}
	if needsDirectory && root == nil {
		if err := outputDirectory.create(ctx); err != nil {
			return printErr("Could not create output directory", err)
		}
		root = outputDirectory.root
	}
	results := make([]bulkJobArtifactResult, 0, len(manifest.Artifacts))
	failed := false
	for _, artifact := range manifest.Artifacts {
		result := bulkJobArtifactResult{Name: artifact.Name}
		if !jobArtifactSelected(artifact.Name, includes, excludes) {
			result.Status = "skipped_filter"
		} else if !strings.HasPrefix(artifact.URI, "obj://") {
			result.Status = "skipped_external"
		} else if ctx.Err() != nil {
			result.Status, result.Error = "not_attempted", "download interrupted or deadline reached"
			failed = true
		} else if verified[artifact.Name] {
			// Recheck before reporting a skip in case another process changed the file.
			info, err := root.Lstat(artifact.Name)
			if err == nil {
				err = verifyExistingJobArtifact(ctx, root, artifact, info)
			}
			if err != nil {
				result.Status, result.Error = "failed", "previously verified file changed or cannot be read; not overwritten"
				failed = true
			} else {
				result.Status, result.Path = "verified_existing", filepath.Join(directory, artifact.Name)
			}
		} else {
			fetchCtx, fetchCancel := context.WithTimeout(ctx, 30*time.Second)
			receipt, fetchErr := client.DownloadJobArtifact(fetchCtx, pos[0], id, index, artifact.Name)
			fetchCancel()
			if fetchErr != nil {
				result.Status, result.Error = "failed", "could not request artifact; inspect the current task and retry"
				failed = true
			} else if receipt.Name != artifact.Name || receipt.SizeBytes != artifact.SizeBytes || strings.TrimPrefix(receipt.SHA256, "sha256:") != strings.TrimPrefix(artifact.SHA256, "sha256:") {
				result.Status, result.Error = "failed", "artifact metadata changed since discovery; no file saved"
				failed = true
			} else if err := saveDownloadedJobArtifact(ctx, client, root, artifact.Name, artifact.Name, receipt); err != nil {
				result.Status, result.Error = "failed", err.Error()
				failed = true
			} else {
				result.Status, result.Path = "saved", filepath.Join(directory, artifact.Name)
			}
		}
		results = append(results, result)
	}
	summary := bulkJobArtifactSummary{}
	for _, result := range results {
		switch result.Status {
		case "saved":
			summary.Saved++
		case "verified_existing":
			summary.VerifiedExisting++
		case "failed":
			summary.Failed++
		case "skipped_external":
			summary.SkippedExternal++
		case "skipped_filter":
			summary.SkippedFilter++
		case "not_attempted":
			summary.NotAttempted++
		}
	}
	resumeCommand := ""
	if failed || ctx.Err() != nil {
		resumeCommand = appendJobArtifactFilters(fmt.Sprintf("%s jobs artifact-download %s %s %d --all --out-dir %s --resume", recoveryCLI(), recoveryArgument(pos[0]), recoveryArgument(id), index, recoveryArgument(directory)), includes, excludes)
	}
	if jsonOutput {
		if code := jsonOut(writeJSONSingle(struct {
			Include       []string                `json:"include,omitempty"`
			Exclude       []string                `json:"exclude,omitempty"`
			Job           string                  `json:"job"`
			RunID         string                  `json:"run_id"`
			TaskIndex     int                     `json:"task_index"`
			Attempt       int                     `json:"attempt"`
			Artifacts     []bulkJobArtifactResult `json:"artifacts"`
			Summary       bulkJobArtifactSummary  `json:"summary"`
			ResumeCommand string                  `json:"resume_command,omitempty"`
		}{includes, excludes, pos[0], id, index, task.Attempt, results, summary, resumeCommand})); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Job: %s; run: %s; task: %d; attempt: %d\n", pos[0], id, index, task.Attempt)
		if len(results) == 0 {
			_, _ = fmt.Fprintln(osStdout, "The recorded manifest contains no artifacts.")
		}
		for _, result := range results {
			_, _ = fmt.Fprintf(osStdout, "%s: %s", oneLine(result.Name), result.Status)
			if result.Path != "" {
				_, _ = fmt.Fprintf(osStdout, "; %s", oneLine(result.Path))
			}
			if result.Error != "" {
				_, _ = fmt.Fprintf(osStdout, "; %s", oneLine(result.Error))
			}
			_, _ = fmt.Fprintln(osStdout)
		}
		_, _ = fmt.Fprintf(osStdout, "Summary: %d saved; %d verified existing; %d failed; %d skipped external; %d not attempted.\n", summary.Saved, summary.VerifiedExisting, summary.Failed, summary.SkippedExternal, summary.NotAttempted)
		if len(includes) > 0 || len(excludes) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Filtered artifacts skipped: %d.\n", summary.SkippedFilter)
		}
		if resumeCommand != "" {
			_, _ = fmt.Fprintf(osStdout, "Resume after addressing the reported failures (POSIX shells):\n  %s\n", resumeCommand)
		}
		_, _ = fmt.Fprintln(osStdout, "Saved and skipped existing files were verified against the discovered size and SHA-256. External artifacts require their storage provider. Already saved files remain after a later failure.")
	}
	if ctx.Err() != nil {
		return printErr("Bulk download interrupted", ctx.Err())
	}
	if failed {
		return printErr("Bulk download incomplete", fmt.Errorf("inspect artifact results; saved files remain and are never overwritten"))
	}
	return 0
}

// Verify through the pinned directory and reject non-regular or replaced files.
func verifyExistingJobArtifact(ctx context.Context, root *os.Root, artifact jobresult.Artifact, before os.FileInfo) error {
	mismatch := func() error {
		return fmt.Errorf("existing artifact %s does not match the recorded size and SHA-256 or changed during verification; no file was overwritten", oneLine(artifact.Name))
	}
	if !before.Mode().IsRegular() || before.Size() != artifact.SizeBytes || artifact.SizeBytes == math.MaxInt64 {
		return mismatch()
	}
	file, err := root.Open(artifact.Name)
	if err != nil {
		return mismatch()
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return mismatch()
	}
	current, err := root.Lstat(artifact.Name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return mismatch()
	}
	hash := sha256.New()
	reader := io.LimitReader(file, artifact.SizeBytes+1)
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			total += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return mismatch()
		}
	}
	after, err := file.Stat()
	if err != nil || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return mismatch()
	}
	current, err = root.Lstat(artifact.Name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(after, current) || current.Size() != after.Size() || !current.ModTime().Equal(after.ModTime()) {
		return mismatch()
	}
	if total != artifact.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != strings.TrimPrefix(artifact.SHA256, "sha256:") {
		return mismatch()
	}
	return nil
}
