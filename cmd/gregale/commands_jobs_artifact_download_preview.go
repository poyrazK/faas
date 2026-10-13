package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/jobresult"
)

type jobArtifactPreviewItem struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Status    string `json:"status"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

func previewBulkJobArtifacts(ctx context.Context, root *os.Root, name, runID string, index, attempt int, directory string, manifest jobresult.Manifest, resume bool, directories, includes, excludes []string) int {
	items := make([]jobArtifactPreviewItem, 0, len(manifest.Artifacts))
	total := new(big.Int)
	downloads, verified, external, conflicts, skipped := 0, 0, 0, 0, 0
	for _, artifact := range manifest.Artifacts {
		if ctx.Err() != nil {
			return printErr("Artifact preview interrupted", ctx.Err())
		}
		item := jobArtifactPreviewItem{Name: artifact.Name, SizeBytes: artifact.SizeBytes}
		if !jobArtifactSelected(artifact.Name, includes, excludes) {
			item.Status = "skipped_filter"
			skipped++
		} else if !strings.HasPrefix(artifact.URI, "obj://") {
			item.Status = "skipped_external"
			external++
		} else if artifact.Name == "." || artifact.Name == ".." || filepath.Base(artifact.Name) != artifact.Name || artifact.SizeBytes == math.MaxInt64 {
			item.Status, item.Error = "conflict", "artifact name or size cannot be used for a local download"
			conflicts++
		} else {
			item.Path = filepath.Join(directory, artifact.Name)
			var info os.FileInfo
			err := os.ErrNotExist
			if root != nil {
				info, err = root.Lstat(artifact.Name)
			}
			if os.IsNotExist(err) {
				item.Status = "would_download"
				downloads++
				total.Add(total, big.NewInt(artifact.SizeBytes))
			} else if err != nil {
				item.Status, item.Error = "conflict", "cannot inspect destination"
				conflicts++
			} else if !resume {
				item.Status, item.Error = "conflict", "destination already exists; no overwrite; use --resume to verify existing files"
				conflicts++
			} else if !info.Mode().IsRegular() {
				item.Status, item.Error = "conflict", "destination must be a regular file, not a symlink or directory"
				conflicts++
			} else if err := verifyExistingJobArtifact(ctx, root, artifact, info); err != nil {
				if ctx.Err() != nil {
					return printErr("Artifact preview interrupted", ctx.Err())
				}
				item.Status, item.Error = "conflict", "existing file does not match the recorded size and SHA-256 or cannot be verified"
				conflicts++
			} else {
				item.Status = "verified_existing"
				verified++
			}
		}
		items = append(items, item)
	}
	if ctx.Err() != nil {
		return printErr("Artifact preview interrupted", ctx.Err())
	}
	command := fmt.Sprintf("%s jobs artifact-download %s %s %d --all --out-dir %s", recoveryCLI(), recoveryArgument(name), recoveryArgument(runID), index, recoveryArgument(directory))
	if resume {
		command += " --resume"
	}
	command = appendJobArtifactFilters(command, includes, excludes)
	receipt := struct {
		Include             []string                 `json:"include,omitempty"`
		Exclude             []string                 `json:"exclude,omitempty"`
		DirectoriesToCreate []string                 `json:"directories_to_create,omitempty"`
		DryRun              bool                     `json:"dry_run"`
		Job                 string                   `json:"job"`
		RunID               string                   `json:"run_id"`
		TaskIndex           int                      `json:"task_index"`
		Attempt             int                      `json:"attempt"`
		Ready               bool                     `json:"ready"`
		Artifacts           []jobArtifactPreviewItem `json:"artifacts"`
		Summary             struct {
			WouldDownload    int         `json:"would_download"`
			DownloadBytes    json.Number `json:"download_bytes"`
			VerifiedExisting int         `json:"verified_existing"`
			SkippedExternal  int         `json:"skipped_external"`
			Conflicts        int         `json:"conflicts"`
			SkippedFilter    int         `json:"skipped_filter,omitempty"`
		} `json:"summary"`
		DownloadCommand string `json:"download_command"`
	}{Include: includes, Exclude: excludes, DirectoriesToCreate: directories, DryRun: true, Job: name, RunID: runID, TaskIndex: index, Attempt: attempt, Ready: conflicts == 0, Artifacts: items, DownloadCommand: command}
	receipt.Summary.WouldDownload = downloads
	receipt.Summary.DownloadBytes = json.Number(total.String())
	receipt.Summary.VerifiedExisting = verified
	receipt.Summary.SkippedExternal = external
	receipt.Summary.Conflicts = conflicts
	receipt.Summary.SkippedFilter = skipped
	if jsonOutput {
		if code := jsonOut(writeJSONSingle(receipt)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Artifact download preview: Job %s; run %s; task %d; attempt %d\n", name, runID, index, attempt)
		for _, path := range directories {
			_, _ = fmt.Fprintf(osStdout, "Would create directory: %s\n", oneLine(path))
		}
		for _, item := range items {
			_, _ = fmt.Fprintf(osStdout, "%s: %s; %d bytes", oneLine(item.Name), item.Status, item.SizeBytes)
			if item.Path != "" {
				_, _ = fmt.Fprintf(osStdout, "; %s", oneLine(item.Path))
			}
			if item.Error != "" {
				_, _ = fmt.Fprintf(osStdout, "; %s", oneLine(item.Error))
			}
			_, _ = fmt.Fprintln(osStdout)
		}
		_, _ = fmt.Fprintf(osStdout, "Summary: %d would download (%s bytes); %d verified existing; %d skipped external; %d conflicts.\n", downloads, total.String(), verified, external, conflicts)
		if len(includes) > 0 || len(excludes) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Filtered artifacts skipped: %d.\n", skipped)
		}
		_, _ = fmt.Fprintf(osStdout, "Download after addressing any conflicts (POSIX shells):\n  %s\n", command)
		_, _ = fmt.Fprintln(osStdout, "Preview only: no signed URLs requested or files written. Destination permissions and remote bytes are checked during download; task outputs and local files may change after this preview.")
	}
	if conflicts > 0 {
		return printErr("Artifact preview has conflicts", fmt.Errorf("address the reported destinations before downloading; no files were written"))
	}
	return 0
}
