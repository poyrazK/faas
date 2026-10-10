package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

type dataAPIDevOptions struct {
	Directory     string `json:"directory"`
	Output        string `json:"output"`
	Port          int    `json:"port"`
	Check         bool   `json:"check"`
	Once          bool   `json:"once"`
	JSON          bool   `json:"json"`
	Watch         bool   `json:"watch"`
	Baseline      string `json:"baseline"`
	CheckBreaking bool   `json:"check_breaking"`
	Scenario      string `json:"scenario"`
	Replay        string `json:"replay"`
	CLI           string `json:"cli"`
}

func cmdDataAPIDev(args []string) int {
	fs := newFlagSet("data-api dev", flag.ContinueOnError)
	directory := fs.String("directory", ".", "Data API starter directory containing migrations/sql")
	output := fs.String("output", "client/src/database.types.ts", "generated types path relative to the project")
	port := fs.Int("port", 8080, "loopback gateway port (0 selects a free port)")
	check := fs.Bool("check", false, "run installed starter client typecheck and tests")
	once := fs.Bool("once", false, "verify the local workflow and exit, removing containers")
	watch := fs.Bool("watch", true, "watch migrations and refresh the local API and types (disabled by --once)")
	baseline := fs.String("baseline", "", "read-only contract baseline (default: last successful local contract)")
	scenario := fs.String("scenario", "", "select one named replay scenario (requires --replay)")
	replay := fs.String("replay", "", "replay saved requests relative to the project (requires --once)")
	checkBreaking := fs.Bool("check-breaking", false, "reject breaking schema changes; requires an existing baseline")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *port < 0 || *port > 65535 || *directory == "" || *output == "" || (*replay != "" && !*once) || (*scenario != "" && *replay == "") {
		PrintUsage(os.Stderr, "usage: gregale data-api dev [--directory DIR] [--output FILE] [--port PORT] [--check] [--once] [--watch=false] [--baseline FILE] [--check-breaking] [--replay FILE --once] [--scenario NAME]", "data-api")
		return 1
	}
	base, err := filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid project directory", err)
	}
	if info, statErr := os.Stat(filepath.Join(base, "migrations", "sql")); statErr != nil || !info.IsDir() {
		return printErr("Invalid project directory", errors.New("migrations/sql must exist; initialize a data-api-starter project first"))
	}
	if *replay != "" {
		*replay = dataAPISyncPath(base, *replay)
		info, replayErr := os.Lstat(*replay)
		if replayErr != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return printErr("Invalid replay collection", errors.New("use an existing regular file under 64 KiB"))
		}
	}
	target := dataAPISyncPath(base, *output)
	if *replay != "" {
		replayInfo, replayErr := os.Stat(*replay)
		outputInfo, outputErr := os.Stat(target)
		if target == *replay || (replayErr == nil && outputErr == nil && os.SameFile(replayInfo, outputInfo)) {
			return printErr("Invalid output", errors.New("types output and replay collection must be different files"))
		}
	}
	contractPath := filepath.Join(base, ".gregale", "data-api-contract.json")
	if *baseline != "" {
		*baseline = dataAPISyncPath(base, *baseline)
		contractPath = *baseline
	}
	baselineInfo, baselineErr := os.Stat(contractPath)
	outputInfo, outputErr := os.Stat(target)
	if target == contractPath || (baselineErr == nil && outputErr == nil && os.SameFile(baselineInfo, outputInfo)) {
		return printErr("Invalid output", errors.New("types output and compatibility baseline must be different files"))
	}
	if *baseline != "" || *checkBreaking {
		if _, err = readDataAPIContractFile(contractPath); err != nil {
			return printErr("Invalid compatibility baseline", fmt.Errorf("%w; initialize a local contract with data-api dev --once or use --baseline FILE", err))
		}
	}
	cli, err := os.Executable()
	if err != nil {
		return printErr("Could not locate the contract comparator", err)
	}
	if info, statErr := os.Lstat(target); statErr == nil && !info.Mode().IsRegular() {
		return printErr("Invalid output", errors.New("types output must be a regular file"))
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return printErr("Invalid output", statErr)
	}
	for _, tool := range []string{"node", "npm", "docker"} {
		if _, err = exec.LookPath(tool); err != nil {
			return printErr("Missing local development prerequisite", fmt.Errorf("install %s before running data-api dev", tool))
		}
	}
	runtime, err := os.MkdirTemp("", "gregale-data-api-dev-")
	if err != nil {
		return printErr("Could not prepare local runtime", err)
	}
	defer func() { _ = os.RemoveAll(runtime) }()
	if err = templates.Materialize("data-api", runtime); err != nil {
		return printErr("Could not prepare local runtime", err)
	}
	for target, source := range map[string]string{
		"migrate.mjs":            "data-api-starter/migrations/migrate.mjs",
		"rpc-permissions.mjs":    "data-api-starter/migrations/rpc-permissions.mjs",
		"rpc-runtime/types.mjs":  "data-api/types.mjs",
		"rpc-runtime/config.mjs": "data-api/config.mjs",
	} {
		content, readErr := templates.FS.ReadFile(source)
		if readErr != nil {
			return printErr("Could not prepare local migrations", readErr)
		}
		path := filepath.Join(runtime, target)
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return printErr("Could not prepare local migrations", err)
		}
		if err = os.WriteFile(path, content, 0o600); err != nil {
			return printErr("Could not prepare local migrations", err)
		}
	}
	options, err := json.Marshal(dataAPIDevOptions{Directory: base, Output: target, Port: *port, Check: *check, Once: *once, JSON: jsonOutput, Watch: *watch, Baseline: *baseline, CheckBreaking: *checkBreaking, CLI: cli, Replay: *replay, Scenario: *scenario})
	if err != nil {
		return printErr("Could not prepare local options", err)
	}
	path := filepath.Join(runtime, "dev-options.json")
	if err = os.WriteFile(path, options, 0o600); err != nil {
		return printErr("Could not prepare local options", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	install, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = runDataAPISyncCommand(install, dataAPISyncCommand{Command: []string{"npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund"}, Directory: runtime})
	cancel()
	if err != nil {
		return printErr("Could not install local runtime dependencies", err)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(runtime, "dev.mjs"), path) //nolint:gosec // Embedded runtime and private options file; no shell.
	command.Dir, command.Stdout, command.Stderr = runtime, osStdout, osStderr
	command.WaitDelay = 210 * time.Second
	// Allow Node's signal handler to remove its containers before force-killing it.
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	err = command.Run()
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		if *replay != "" {
			return printErr("Saved request replay interrupted", ctx.Err())
		}
		return 0
	}
	if err != nil {
		return printErr("Local Data API failed", err)
	}
	return 0
}
