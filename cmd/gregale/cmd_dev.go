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
	devWatchPollInterval   = 250 * time.Millisecond
	devWatchSettleInterval = 500 * time.Millisecond
	devSessionFunction     = "function"
)

type devSourceConfig struct {
	shape   shape
	runtime string
	handler string
}

func resolveDevSourceConfig(sourceDir string) (devSourceConfig, error) {
	resolvedShape, runtime, handler, err := resolveDeployShape(sourceDir, false, false, jsonOutput)
	if err != nil {
		return devSourceConfig{}, err
	}
	return devSourceConfig{shape: resolvedShape, runtime: runtime, handler: handler}, nil
}

func (c devSourceConfig) sessionRequest(workspaceID string) api.UpsertDevSessionRequest {
	req := api.UpsertDevSessionRequest{WorkspaceID: workspaceID}
	if c.shape == shapeFunction {
		req.Type = devSessionFunction
		req.Runtime = c.runtime
	}
	return req
}

func (c devSourceConfig) deployArgs(slug, sourceDir string) []string {
	args := []string{"--name", slug, "--path", sourceDir, "--worktree"}
	if c.shape == shapeFunction {
		return append(args, "--function", "--runtime", c.runtime, "--handler", c.handler)
	}
	return append(args, "--app")
}

// devLoopOps keeps the watch-state machine independently testable. Production
// closures below own the API client and terminal rendering; the loop itself
// only decides when a failed sync may be retried and when source configuration
// must be resolved again.
type devLoopOps struct {
	deploy          func(devSourceConfig) int
	waitForChange   func(context.Context, string, [sha256.Size]byte) ([sha256.Size]byte, error)
	resolve         func(string) (devSourceConfig, error)
	refresh         func(devSourceConfig) error
	onWatching      func()
	onChange        func()
	onDeployFailed  func(int)
	onResolveFailed func(error)
	onRefreshFailed func(error)
	onWatchFailed   func(error) int
}

func runDevWatchLoop(ctx context.Context, sourceDir string, previous [sha256.Size]byte, initial devSourceConfig, once bool, ops devLoopOps) int {
	code := ops.deploy(initial)
	if once {
		return code
	}
	if code != 0 && ops.onDeployFailed != nil {
		ops.onDeployFailed(code)
	}

	for {
		if ops.onWatching != nil {
			ops.onWatching()
		}
		next, err := ops.waitForChange(ctx, sourceDir, previous)
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			if ops.onWatchFailed != nil {
				return ops.onWatchFailed(err)
			}
			return 1
		}
		// A failed build is still a handled snapshot. Wait for another edit
		// instead of repeatedly redeploying the same broken source.
		previous = next

		config, err := ops.resolve(sourceDir)
		if err != nil {
			if ops.onResolveFailed != nil {
				ops.onResolveFailed(err)
			}
			continue
		}
		if err := ops.refresh(config); err != nil {
			if ops.onRefreshFailed != nil {
				ops.onRefreshFailed(err)
			}
			continue
		}
		if ops.onChange != nil {
			ops.onChange()
		}
		code = ops.deploy(config)
		if code != 0 && ops.onDeployFailed != nil {
			ops.onDeployFailed(code)
		}
	}
}

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
	developerID, err := loadOrCreateDeveloperID()
	if err != nil {
		return printErr("Could not load local developer identity", err)
	}
	workspaceID, err := deriveDevWorkspaceID(developerID, sourceDir)
	if err != nil {
		return printErr("Could not identify developer workspace", err)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *stop {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := client.DestroyDevSession(ctx, project, workspaceID); err != nil {
			return printErr("Could not stop developer environment", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]string{"project": project, "status": "stopped"}))
		}
		PrintOK(osStdout, "Developer environment %s stopped.", project)
		return 0
	}

	config, err := resolveDevSourceConfig(sourceDir)
	if err != nil {
		return printErr("No deployable source found in "+filepath.Base(sourceDir), err)
	}

	session, err := upsertDevSession(client, project, config.sessionRequest(workspaceID))
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
	return runDevWatchLoop(ctx, sourceDir, lastSynced, config, *once, devLoopOps{
		deploy: func(config devSourceConfig) int {
			started := time.Now()
			code := cmdDeployTarballToExisting(config.deployArgs(session.App.Slug, sourceDir), true)
			if code == 0 && !jsonOutput {
				PrintOK(osStdout, "Developer sync live in %s.", time.Since(started).Round(100*time.Millisecond))
			}
			return code
		},
		waitForChange: waitForDevSourceChange,
		resolve:       resolveDevSourceConfig,
		refresh: func(config devSourceConfig) error {
			refreshed, refreshErr := upsertDevSession(client, project, config.sessionRequest(workspaceID))
			if refreshErr == nil {
				session = refreshed
			}
			return refreshErr
		},
		onWatching: func() {
			if !jsonOutput {
				PrintProgress(osStdout, "watching for changes (Ctrl-C to stop watching; environment remains available)")
			}
		},
		onChange: func() {
			if !jsonOutput {
				PrintProgress(osStdout, "change detected; syncing to %s", session.App.URL)
			}
		},
		onDeployFailed: func(_ int) {
			if !jsonOutput {
				PrintWarn(osStderr, "developer sync failed; fix the source and save to retry (environment remains available at %s)", session.App.URL)
			}
		},
		onResolveFailed: func(resolveErr error) {
			_ = printErr("Could not prepare developer sync", resolveErr)
			if !jsonOutput {
				PrintWarn(osStderr, "sync skipped; fix the source and save to retry")
			}
		},
		onRefreshFailed: func(refreshErr error) {
			_ = printErr("Could not refresh developer environment", refreshErr)
			if !jsonOutput {
				PrintWarn(osStderr, "sync skipped; save again to retry")
			}
		},
		onWatchFailed: func(waitErr error) int {
			return printErr("Could not watch developer source", waitErr)
		},
	})
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
	return waitForDevSourceChangeWithIntervals(ctx, sourceDir, previous, devWatchPollInterval, devWatchSettleInterval)
}

func waitForDevSourceChangeWithIntervals(ctx context.Context, sourceDir string, previous [sha256.Size]byte, pollInterval, settleInterval time.Duration) ([sha256.Size]byte, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	candidate := previous
	var stableSince time.Time
	for {
		select {
		case <-ctx.Done():
			return previous, ctx.Err()
		case <-ticker.C:
			current, err := devSourceFingerprint(sourceDir)
			if err != nil {
				return previous, err
			}
			if current != candidate {
				candidate = current
				if current == previous {
					stableSince = time.Time{}
				} else {
					stableSince = time.Now()
				}
				continue
			}
			if current != previous && !stableSince.IsZero() && time.Since(stableSince) >= settleInterval {
				return current, nil
			}
		}
	}
}
