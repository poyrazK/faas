package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

type dataAPIFixtureOptions struct {
	Config         string `json:"config"`
	Output         string `json:"output"`
	DatabaseURLEnv string `json:"database_url_env"`
	APIRole        string `json:"api_role,omitempty"`
	JSON           bool   `json:"json"`
}

func cmdDataAPIFixture(args []string) int {
	if len(args) == 0 || args[0] != "build" {
		return printErr("Invalid fixture command", errors.New("use data-api fixture build --config FILE --output DIR"))
	}
	fs := newFlagSet("data-api fixture build", flag.ContinueOnError)
	config := fs.String("config", "", "JSON column allowlists, masks and root filters")
	output := fs.String("output", "", "new fixture package directory")
	env := fs.String("database-url-env", "DATA_API_FIXTURE_DATABASE_URL", "environment variable holding the local or test PostgreSQL URL")
	role := fs.String("api-role", "", "source API role used for contract introspection")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *config == "" || *output == "" || !dataAPITokenEnvName.MatchString(*env) {
		return printErr("Invalid fixture options", errors.New("provide --config FILE, --output DIR and a valid database URL environment variable name"))
	}
	input, err := filepath.Abs(*config)
	if err != nil {
		return printErr("Invalid config path", err)
	}
	target, err := filepath.Abs(*output)
	if err != nil {
		return printErr("Invalid output path", err)
	}
	file, err := openCustomerFile(input)
	if err != nil {
		return printErr("Invalid fixture config", err)
	}
	info, err := file.Stat()
	_ = file.Close()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return printErr("Invalid fixture config", errors.New("use a regular JSON file under 64 KiB"))
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return printErr("Invalid fixture output", errors.New("choose a new directory; existing packages are preserved"))
	}
	if os.Getenv(*env) == "" {
		return printErr("Missing fixture source", errors.New("configure the selected database URL environment variable"))
	}
	return runDataAPIFixture(dataAPIFixtureOptions{Config: input, Output: target, DatabaseURLEnv: *env, APIRole: *role, JSON: jsonOutput})
}

func runDataAPIFixture(options dataAPIFixtureOptions) int {
	runtime, err := os.MkdirTemp("", "gregale-data-api-fixture-")
	if err != nil {
		return printErr("Could not prepare fixture runtime", err)
	}
	defer func() { _ = os.RemoveAll(runtime) }()
	if err := templates.Materialize("data-api", runtime); err != nil {
		return printErr("Could not prepare fixture runtime", err)
	}
	content, err := json.Marshal(options)
	if err != nil {
		return printErr("Invalid fixture options", err)
	}
	path := filepath.Join(runtime, "fixture-options.json")
	if err := os.WriteFile(path, content, 0600); err != nil {
		return printErr("Could not prepare fixture options", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	install, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = runDataAPISyncCommand(install, dataAPISyncCommand{Command: []string{"npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund"}, Directory: runtime})
	cancel()
	if err != nil {
		return printErr("Could not install fixture runtime", err)
	}
	run, cancelRun := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelRun()
	command := exec.CommandContext(run, "node", filepath.Join(runtime, "fixture-builder.mjs"), path) //nolint:gosec // Embedded runtime and private options; no shell or URL arguments.
	command.Dir, command.Stdout, command.Stderr = runtime, osStdout, osStderr
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		return printErr("Fixture build failed", errors.New("review the fixture config, source access, bounds and new output directory"))
	}
	return 0
}
