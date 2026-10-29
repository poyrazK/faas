package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsArtifactDownload(args []string) int {
	// Flags after -- are artifact names or other literal positional values.
	separator := len(args)
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	remaining, taskFlag, taskSet, err := splitJobLogTask(args[:separator])
	if err != nil {
		return printErr("Invalid task selection", err)
	}
	args = append(remaining, args[separator:]...)
	fs := newFlagSet("jobs artifact-download", flag.ContinueOnError)
	includes := registerJobsMultiFlag(fs, "include", "repeatable artifact-name glob for bulk downloads; quote patterns")
	excludes := registerJobsMultiFlag(fs, "exclude", "repeatable artifact-name glob to omit; exclusions take precedence")
	allTasks := fs.Bool("all-tasks", false, "download current managed artifacts across all task pages")
	dryRun := fs.Bool("dry-run", false, "preview bulk destinations and conflicts without downloads; requires --all or --all-tasks")
	resume := fs.Bool("resume", false, "verify and skip matching files; requires --all or --all-tasks and an explicit run UUID")
	all := fs.Bool("all", false, "download all current managed artifacts")
	outDir := fs.String("out-dir", "", "destination directory for bulk downloads (created when missing)")
	out := fs.String("out", "", "new local output file (required; no overwrite)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	pos := fs.Args()
	patterns := []string(*includes)
	exclusions := []string(*excludes)
	if err := validateJobArtifactExcludes(exclusions); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if err := validateJobArtifactIncludes(patterns); err != nil {
		return printErr("Invalid artifact filter", err)
	}
	if len(exclusions) > 0 && !*all && !*allTasks {
		return printErr("Invalid artifact filter", fmt.Errorf("--exclude requires --all or --all-tasks"))
	}
	if len(patterns) > 0 && !*all && !*allTasks {
		return printErr("Invalid artifact filter", fmt.Errorf("--include requires --all or --all-tasks"))
	}
	if taskSet && (*all || *allTasks) {
		return printErr("Invalid download target", fmt.Errorf("--task supports a single named artifact; use positional targets for --all or --all-tasks"))
	}
	if *allTasks {
		if *all || *out != "" {
			return printErr("Invalid whole-run download", fmt.Errorf("--all-tasks uses --out-dir and cannot be combined with --all or --out"))
		}
		return downloadJobRunArtifactsWithFilters(pos, *outDir, *resume, *dryRun, patterns, exclusions, nil, nil)
	}
	if *all {
		if *out != "" {
			return printErr("Invalid bulk download", fmt.Errorf("--all uses --out-dir, not --out"))
		}
		return downloadAllJobArtifacts(pos, *outDir, *resume, *dryRun, patterns, exclusions)
	}
	if *dryRun {
		return printErr("Invalid download", fmt.Errorf("--dry-run requires --all or --all-tasks"))
	}
	if *resume {
		return printErr("Invalid download", fmt.Errorf("--resume requires --all or --all-tasks and an explicit run UUID"))
	}
	if *outDir != "" {
		return printErr("Invalid download", fmt.Errorf("--out-dir requires --all or --all-tasks"))
	}
	if taskSet {
		if len(pos) < 1 || len(pos) > 3 {
			return printErr("Invalid artifact download", fmt.Errorf("use jobs artifact-download [JOB] [RUN_ID|latest] ARTIFACT_NAME --task N --out NEW_FILE; do not combine --task with a positional task index"))
		}
		artifactName := pos[len(pos)-1]
		target := append([]string(nil), pos[:len(pos)-1]...)
		if len(target) == 0 {
			defaults, path, err := readJobDefaults()
			if err != nil {
				return printErr("Could not read Job defaults", err)
			}
			target = []string{defaults.Job}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: job=%s\n", path, defaults.Job)
			}
		}
		if len(target) == 1 {
			target = append(target, "latest")
		}
		pos = append(target, taskFlag, artifactName)
	}
	if len(pos) != 4 || !jobSlugPattern.MatchString(pos[0]) || (pos[1] != "latest" && !jobRunIDPattern.MatchString(pos[1])) || pos[3] == "" || *out == "" || *out == "-" {
		return printErr("Invalid artifact download", fmt.Errorf("usage: gregale job artifact download JOB RUN_ID|latest TASK_INDEX ARTIFACT_NAME --out NEW_FILE"))
	}
	index, err := strconv.Atoi(pos[2])
	if err != nil || index < 0 {
		return printErr("Invalid task index", fmt.Errorf("provide a non-negative task index"))
	}
	output, err := filepath.Abs(*out)
	if err != nil {
		return printErr("Invalid output path", err)
	}
	root, err := os.OpenRoot(filepath.Dir(output))
	if err != nil {
		return printErr("Could not open output directory", err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(output)
	if _, err := root.Lstat(name); err == nil || !os.IsNotExist(err) {
		return printErr("Cannot save artifact", fmt.Errorf("output must be a new file in an existing directory; existing files and symlinks are not overwritten"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 5*time.Minute)
	defer cancel()
	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	id, err := resolveJobRunReadArg(readCtx, client, pos[0], pos[1])
	if err != nil {
		readCancel()
		return printErr("Could not resolve Job run", err)
	}
	artifact, err := client.DownloadJobArtifact(readCtx, pos[0], id, index, pos[3])
	readCancel()
	if err != nil {
		return printErr("Artifact request failed", err)
	}
	if err := saveDownloadedJobArtifact(ctx, client, root, name, pos[3], artifact); err != nil {
		return printErr("Could not download verified artifact", err)
	}
	receipt := struct {
		Job       string `json:"job"`
		RunID     string `json:"run_id"`
		TaskIndex int    `json:"task_index"`
		Artifact  string `json:"artifact"`
		Path      string `json:"path"`
		SizeBytes int64  `json:"size_bytes"`
		SHA256    string `json:"sha256"`
	}{pos[0], id, index, artifact.Name, output, artifact.SizeBytes, artifact.SHA256}
	if jsonOutput {
		return jsonOut(writeJSONSingle(receipt))
	}
	PrintOK(osStdout, "Saved verified artifact %s from run %s task %d to %s (%d bytes).", oneLine(artifact.Name), id, index, oneLine(output), artifact.SizeBytes)
	return 0
}

func saveDownloadedJobArtifact(ctx context.Context, client *api.Client, root *os.Root, outputName, artifactName string, artifact api.JobArtifactDownloadResponse) error {
	digest := strings.TrimPrefix(artifact.SHA256, "sha256:")
	expected, err := hex.DecodeString(digest)
	if artifact.Name != artifactName || artifact.SizeBytes < 0 || artifact.SizeBytes == math.MaxInt64 || err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("artifact response did not identify the requested name, valid size, and SHA-256")
	}
	signed := artifact.Download
	u, err := url.Parse(signed.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback())) || signed.Method != "GET" {
		return fmt.Errorf("artifact response did not provide a valid signed GET URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signed.URL, nil)
	if err != nil {
		return fmt.Errorf("could not prepare artifact download")
	}
	for name, value := range signed.Headers {
		request.Header.Set(name, value)
	}
	request.Header.Set("Accept-Encoding", "identity")
	downloader := *client.HTTPClient()
	downloader.Timeout = 5 * time.Minute
	downloader.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := downloader.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("artifact transfer failed; request a fresh download and retry")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("artifact transfer returned HTTP %d; request a fresh download and retry", response.StatusCode)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("could not prepare temporary output")
	}
	temporary := ".gregale-artifact-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	defer func() { _ = root.Remove(temporary) }()
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, artifact.SizeBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("artifact transfer or temporary-file write failed")
	}
	if n != artifact.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(digest) {
		return fmt.Errorf("downloaded artifact failed size or SHA-256 verification; no output was saved")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync verified artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close verified artifact: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Link(temporary, outputName); err != nil {
		return fmt.Errorf("save verified artifact without overwrite: %w", err)
	}
	return nil
}
