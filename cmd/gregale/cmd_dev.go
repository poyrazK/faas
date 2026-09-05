package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	devWatchPollInterval = 750 * time.Millisecond
	devSessionFunction   = "function"
)

// cmdDev provides the preview-like inner loop for local source: reserve one
// stable remote environment, upload the dirty working tree, then redeploy when
// deployable files change. --once is useful for scripts; --stop tears the
// environment down explicitly instead of waiting for its lease to expire.
func cmdDev(args []string) int {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	name := fs.String("name", "", "developer-session project name (default: selected source directory)")
	sourcePath := fs.String("path", "", "source directory (relative to the current directory)")
	once := fs.Bool("once", false, "deploy once and exit instead of watching for changes")
	stop := fs.Bool("stop", false, "tear down this project's developer environment")
	if err := fs.Parse(args); err != nil {
		PrintUsage(osStderr, "usage: gregale dev [--path DIR] [--name PROJECT] [--once|--stop]", "dev")
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale dev [--path DIR] [--name PROJECT] [--once|--stop]", "dev")
		return 1
	}
	if *once && *stop {
		return printErr("Invalid flags", fmt.Errorf("--once and --stop are mutually exclusive"))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	sourceDir, err := resolveDeploySourceDir(cwd, *sourcePath)
	if err != nil {
		return printErr("Invalid developer source", err)
	}
	project := *name
	if project == "" {
		project = sanitizeSlug(filepath.Base(sourceDir))
	}
	if project != sanitizeSlug(project) || len(project) < 3 || len(project) > 40 {
		return printErr("Invalid --name", fmt.Errorf("use 3–40 lowercase letters, digits, and hyphens"))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *stop {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := client.DestroyDevSession(ctx, project); err != nil {
			return printErr("Could not stop developer environment", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]string{"project": project, "status": "stopped"}))
		}
		PrintOK(osStdout, "Developer environment %s stopped.", project)
		return 0
	}

	resolvedShape, runtime, handler, err := resolveDeployShape(sourceDir, false, false, jsonOutput)
	if err != nil {
		return printErr("No deployable source found in "+filepath.Base(sourceDir), err)
	}
	sessionReq := api.UpsertDevSessionRequest{}
	if resolvedShape == shapeFunction {
		sessionReq.Type = devSessionFunction
		sessionReq.Runtime = runtime
	}

	session, err := upsertDevSession(client, project, sessionReq)
	if err != nil {
		return printErr("Could not create developer environment", err)
	}
	if !jsonOutput {
		PrintOK(osStdout, "Developer environment: %s", session.App.URL)
		PrintProgress(osStdout, "lease expires %s after the latest sync", session.ExpiresAt.Local().Format(time.RFC822))
	}

	ctx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignal()
	lastSynced, err := devSourceFingerprint(sourceDir)
	if err != nil {
		return printErr("Could not watch developer source", err)
	}
	for {
		deployArgs := []string{"--name", session.App.Slug, "--path", sourceDir, "--worktree"}
		if resolvedShape == shapeFunction {
			deployArgs = append(deployArgs, "--function", "--runtime", runtime, "--handler", handler)
		} else {
			deployArgs = append(deployArgs, "--app")
		}
		if code := cmdDeployTarballToExisting(deployArgs, true); code != 0 {
			return code
		}
		if *once {
			return 0
		}
		if !jsonOutput {
			PrintProgress(osStdout, "watching for changes (Ctrl-C to stop watching; environment remains available)")
		}
		next, waitErr := waitForDevSourceChange(ctx, sourceDir, lastSynced)
		if waitErr != nil {
			if ctx.Err() != nil {
				return 0
			}
			return printErr("Could not watch developer source", waitErr)
		}
		lastSynced = next
		session, err = upsertDevSession(client, project, sessionReq)
		if err != nil {
			return printErr("Could not refresh developer environment", err)
		}
		if !jsonOutput {
			PrintProgress(osStdout, "change detected; syncing to %s", session.App.URL)
		}
	}
}

func upsertDevSession(client *Client, project string, req api.UpsertDevSessionRequest) (api.DevSessionResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return client.UpsertDevSession(ctx, project, req)
}

// devSourceFingerprint hashes only metadata for files the deploy packer would
// include. This keeps the polling loop cheap for larger repositories while
// still detecting normal editor writes, renames, creates, and deletes.
func devSourceFingerprint(sourceDir string) ([sha256.Size]byte, error) {
	h := sha256.New()
	patterns := loadGregaleignore(sourceDir)
	err := filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceDir {
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if shouldExclude(rel, entry.IsDir(), patterns) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano(), info.Mode())
		return nil
	})
	var sum [sha256.Size]byte
	if err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func waitForDevSourceChange(ctx context.Context, sourceDir string, previous [sha256.Size]byte) ([sha256.Size]byte, error) {
	ticker := time.NewTicker(devWatchPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return previous, ctx.Err()
		case <-ticker.C:
			current, err := devSourceFingerprint(sourceDir)
			if err != nil {
				return previous, err
			}
			if current != previous {
				return current, nil
			}
		}
	}
}
