package main

import (
	"context"
	"crypto/sha256"
	"errors"
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
// coordinates source changes, in-flight cancellation, retry, and config
// resolution.
type devLoopOps struct {
	deploy          func(context.Context, devSourceConfig, func(string)) int
	cancelDeploy    func(context.Context, string) error
	waitForChange   func(context.Context, string, [sha256.Size]byte) ([sha256.Size]byte, error)
	resolve         func(string) (devSourceConfig, error)
	refresh         func(devSourceConfig) error
	onWatching      func()
	onChange        func()
	onLive          func()
	onSuperseded    func()
	onDeployFailed  func(int)
	onCancelFailed  func(error)
	onResolveFailed func(error)
	onRefreshFailed func(error)
	onWatchFailed   func(error) int
}

type devSourceChange struct {
	err error
}

type devDeployResult struct {
	code         int
	deploymentID string
}

type activeDevDeploy struct {
	cancel          context.CancelFunc
	queued          <-chan string
	done            <-chan devDeployResult
	deploymentID    string
	superseded      bool
	cancelAttempted bool
}

func watchDevSource(ctx context.Context, sourceDir string, previous [sha256.Size]byte, waitForChange func(context.Context, string, [sha256.Size]byte) ([sha256.Size]byte, error)) <-chan devSourceChange {
	changes := make(chan devSourceChange, 1)
	go func() {
		for {
			next, err := waitForChange(ctx, sourceDir, previous)
			event := devSourceChange{err: err}
			select {
			case changes <- event:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
			previous = next
		}
	}()
	return changes
}

func startDevDeploy(ctx context.Context, config devSourceConfig, deploy func(context.Context, devSourceConfig, func(string)) int) *activeDevDeploy {
	deployCtx, cancel := context.WithCancel(ctx)
	queued := make(chan string, 1)
	done := make(chan devDeployResult, 1)
	go func() {
		deploymentID := ""
		code := deploy(deployCtx, config, func(id string) {
			deploymentID = id
			select {
			case queued <- id:
			case <-deployCtx.Done():
			}
		})
		done <- devDeployResult{code: code, deploymentID: deploymentID}
	}()
	return &activeDevDeploy{cancel: cancel, queued: queued, done: done}
}

func runDevWatchLoop(ctx context.Context, sourceDir string, previous [sha256.Size]byte, initial devSourceConfig, once bool, ops devLoopOps) int {
	if once {
		return ops.deploy(ctx, initial, nil)
	}

	changes := watchDevSource(ctx, sourceDir, previous, ops.waitForChange)
	if ops.onWatching != nil {
		ops.onWatching()
	}
	current := startDevDeploy(ctx, initial, ops.deploy)
	queued := current.queued
	done := current.done
	pending := false

	cancelCurrent := func() {
		if current == nil || current.cancelAttempted || current.deploymentID == "" {
			return
		}
		current.cancelAttempted = true
		if ops.cancelDeploy != nil {
			if err := ops.cancelDeploy(ctx, current.deploymentID); err != nil && ops.onCancelFailed != nil {
				ops.onCancelFailed(err)
			}
		}
		current.cancel()
	}

	startPending := func() {
		config, err := ops.resolve(sourceDir)
		if err != nil {
			if ops.onResolveFailed != nil {
				ops.onResolveFailed(err)
			}
			return
		}
		if err := ops.refresh(config); err != nil {
			if ops.onRefreshFailed != nil {
				ops.onRefreshFailed(err)
			}
			return
		}
		if ops.onChange != nil {
			ops.onChange()
		}
		current = startDevDeploy(ctx, config, ops.deploy)
		queued = current.queued
		done = current.done
	}

	for {
		select {
		case <-ctx.Done():
			return 0

		case change := <-changes:
			if change.err != nil {
				if ctx.Err() != nil {
					return 0
				}
				if ops.onWatchFailed != nil {
					return ops.onWatchFailed(change.err)
				}
				return 1
			}
			// A failed or superseded build is still a handled snapshot. Wait
			// for another edit instead of repeatedly sending the same source.
			if current == nil {
				startPending()
				continue
			}
			pending = true
			if !current.superseded {
				current.superseded = true
				if ops.onSuperseded != nil {
					ops.onSuperseded()
				}
			}
			cancelCurrent()

		case id := <-queued:
			if current == nil {
				continue
			}
			current.deploymentID = id
			queued = nil
			if current.superseded {
				cancelCurrent()
			}

		case result := <-done:
			if current == nil {
				continue
			}
			if current.deploymentID == "" {
				current.deploymentID = result.deploymentID
			}
			if current.superseded {
				cancelCurrent()
			} else if ctx.Err() == nil && result.code == 0 && ops.onLive != nil {
				ops.onLive()
			} else if ctx.Err() == nil && result.code != 0 && ops.onDeployFailed != nil {
				ops.onDeployFailed(result.code)
			}
			current.cancel()
			current = nil
			queued = nil
			done = nil
			if ctx.Err() != nil {
				return 0
			}
			if !pending {
				continue
			}
			pending = false
			// Consume source events already waiting behind cancellation so
			// the next upload is prepared from the newest settled tree.
		drainChanges:
			for {
				select {
				case change := <-changes:
					if change.err != nil {
						if ctx.Err() != nil {
							return 0
						}
						if ops.onWatchFailed != nil {
							return ops.onWatchFailed(change.err)
						}
						return 1
					}
				default:
					break drainChanges
				}
			}
			startPending()
		}
	}
}

// cmdDev provides the preview-like inner loop for local source: reserve one
// stable remote environment, upload the dirty working tree, then redeploy when
// deployable files change. --once is useful for scripts; --stop tears the
// environment down explicitly instead of waiting for its lease to expire.
func cmdDev(args []string) int {
	if len(args) > 0 && args[0] == "status" {
		if len(args) != 1 {
			PrintUsage(osStderr, "usage: gregale dev status", "dev")
			return 1
		}
		return cmdDevStatus()
	}
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	name := fs.String("name", "", "developer-session project name (default: selected source directory)")
	sourcePath := fs.String("path", "", "source directory (relative to the current directory)")
	envFile := fs.String("env-file", "", "sync KEY=VALUE entries as developer secrets (explicit opt-in)")
	once := fs.Bool("once", false, "deploy once and exit instead of watching for changes")
	stop := fs.Bool("stop", false, "tear down this project's developer environment")
	noLogs := fs.Bool("no-logs", false, "do not attach the live runtime log stream")
	if err := fs.Parse(args); err != nil {
		PrintUsage(osStderr, "usage: gregale dev [--path DIR] [--name PROJECT] [--env-file PATH] [--once|--stop] [--no-logs]", "dev")
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(osStderr, "usage: gregale dev [--path DIR] [--name PROJECT] [--env-file PATH] [--once|--stop] [--no-logs]", "dev")
		return 1
	}
	if *once && *stop {
		return printErr("Invalid flags", fmt.Errorf("--once and --stop are mutually exclusive"))
	}
	if *stop && *envFile != "" {
		return printErr("Invalid flags", fmt.Errorf("--env-file cannot be combined with --stop"))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	sourceDir, err := resolveDeploySourceDir(cwd, *sourcePath)
	if err != nil {
		return printErr("Invalid developer source", err)
	}
	envFilePath, err := resolveDevEnvFilePath(cwd, *envFile)
	if err != nil {
		return printErr("Invalid developer env file", err)
	}
	if envFilePath != "" {
		if _, _, err := readDevEnvFile(envFilePath); err != nil {
			return printErr("Invalid developer env file", err)
		}
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
	runtimeLogCtx, cancelRuntimeLogs := context.WithCancel(ctx)
	defer cancelRuntimeLogs()
	runtimeLogsStarted := false
	lastSynced, err := devSourceFingerprint(sourceDir, envFilePath)
	if err != nil {
		return printErr("Could not watch developer source", err)
	}
	syncState := &devSourceSyncState{}
	envSyncState := &devEnvSyncState{}
	waitForChange := func(waitCtx context.Context, dir string, previous [sha256.Size]byte) ([sha256.Size]byte, error) {
		if envFilePath != "" {
			return waitForDevSourceChange(waitCtx, dir, previous, envFilePath)
		}
		return waitForDevSourceChange(waitCtx, dir, previous)
	}
	return runDevWatchLoop(ctx, sourceDir, lastSynced, config, *once, devLoopOps{
		deploy: func(deployCtx context.Context, config devSourceConfig, queued func(string)) int {
			if envFilePath != "" {
				report, syncErr := envSyncState.sync(deployCtx, client, session.App.Slug, envFilePath)
				if syncErr != nil {
					_ = printErr("Could not sync developer config", syncErr)
					return 1
				}
				if report.Changed && !jsonOutput {
					PrintProgress(osStdout, "%s", report.progressLine())
				}
			}
			started := time.Now()
			execution := deployExecution{
				developerSource: syncState,
				extraSourceExcludes: func() []string {
					if envFilePath == "" {
						return nil
					}
					return []string{envFilePath}
				}(),
				onQueued: func(dep api.DeploymentResponse) {
					if queued != nil {
						queued(dep.ID)
					}
				},
			}
			code := cmdDeployTarballToExisting(deployCtx, config.deployArgs(session.App.Slug, sourceDir), true, execution)
			if code == 0 && !jsonOutput {
				PrintOK(osStdout, "Developer sync live in %s.", time.Since(started).Round(100*time.Millisecond))
			}
			return code
		},
		onLive: func() {
			if *once || *noLogs || jsonOutput || runtimeLogsStarted {
				return
			}
			runtimeLogsStarted = true
			PrintProgress(osStdout, "runtime logs attached (Ctrl-C to stop watching)")
			go followDevRuntimeLogs(runtimeLogCtx, client, session.App.Slug)
		},
		cancelDeploy: func(cancelCtx context.Context, deploymentID string) error {
			cancelCtx, cancel := context.WithTimeout(cancelCtx, 15*time.Second)
			defer cancel()
			_, cancelErr := client.CancelDeployment(cancelCtx, session.App.Slug, deploymentID, "user")
			if devDeploymentAlreadyTerminal(cancelErr) {
				return nil
			}
			return cancelErr
		},
		waitForChange: waitForChange,
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
		onSuperseded: func() {
			if !jsonOutput {
				PrintProgress(osStdout, "newer change detected; superseding in-flight sync")
			}
		},
		onDeployFailed: func(_ int) {
			if !jsonOutput {
				PrintWarn(osStderr, "developer sync failed; fix the source and save to retry (environment remains available at %s)", session.App.URL)
			}
		},
		onCancelFailed: func(cancelErr error) {
			if !jsonOutput {
				PrintWarn(osStderr, "could not cancel obsolete developer sync (%v); continuing with the latest source", cancelErr)
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

// cmdDevStatus reports the account-wide developer-environment budget. It is
// intentionally sourced from whoami so scripts and the dashboard use the
// same authoritative count and plan limit without needing to know workspace
// identity details.
func cmdDevStatus() int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	account, err := client.Whoami(ctx)
	if err != nil {
		return printErr("Could not load developer environment status", err)
	}
	status := struct {
		Plan      string `json:"plan"`
		Used      int    `json:"used"`
		Limit     int    `json:"limit"`
		Available int    `json:"available"`
	}{
		Plan:      account.Plan,
		Used:      account.DeveloperAppCount,
		Limit:     account.Limits.DeveloperApps,
		Available: account.Limits.DeveloperApps - account.DeveloperAppCount,
	}
	if status.Available < 0 {
		status.Available = 0
	}
	if jsonOutput {
		if err := writeJSON(status); err != nil {
			return jsonOut(err)
		}
		return 0
	}
	PrintOK(osStdout, "Developer environments: %d/%d used (%d available).", status.Used, status.Limit, status.Available)
	return 0
}

func devDeploymentAlreadyTerminal(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Problem.Code == api.CodeDeploymentCancelLiveForbidden ||
		apiErr.Problem.Code == api.CodeDeploymentCancelNotCancellable
}

func upsertDevSession(client *Client, project string, req api.UpsertDevSessionRequest) (api.DevSessionResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return client.UpsertDevSession(ctx, project, req)
}

// devSourceFingerprint hashes only metadata for files the deploy packer would
// include. This keeps the polling loop cheap for larger repositories while
// still detecting normal editor writes, renames, creates, and deletes.
func devSourceFingerprint(sourceDir string, extraFiles ...string) ([sha256.Size]byte, error) {
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
	for _, extra := range extraFiles {
		digest, digestErr := devEnvFileFingerprint(extra)
		if digestErr != nil {
			return sum, digestErr
		}
		_, _ = fmt.Fprintf(h, "extra\x00%s\x00%x\n", filepath.Clean(extra), digest)
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func waitForDevSourceChange(ctx context.Context, sourceDir string, previous [sha256.Size]byte, extraFiles ...string) ([sha256.Size]byte, error) {
	return waitForDevSourceChangeWithIntervals(ctx, sourceDir, previous, devWatchPollInterval, devWatchSettleInterval, extraFiles...)
}

func waitForDevSourceChangeWithIntervals(ctx context.Context, sourceDir string, previous [sha256.Size]byte, pollInterval, settleInterval time.Duration, extraFiles ...string) ([sha256.Size]byte, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	candidate := previous
	var stableSince time.Time
	for {
		select {
		case <-ctx.Done():
			return previous, ctx.Err()
		case <-ticker.C:
			current, err := devSourceFingerprint(sourceDir, extraFiles...)
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
