// adr: 650 — compose existing migration, restart and type-export lifecycles.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPISyncCommand struct {
	Command   []string `json:"command"`
	Directory string   `json:"directory,omitempty"`
}

type dataAPISyncConfig struct {
	Output         string              `json:"output"`
	Migrate        dataAPISyncCommand  `json:"migrate"`
	Check          dataAPISyncCommand  `json:"check"`
	Permissions    *dataAPISyncCommand `json:"permissions,omitempty"`
	AccessTokenEnv string              `json:"access_token_env,omitempty"`
	accessToken    string
}

type dataAPISyncReceipt struct {
	App              string `json:"app"`
	WakeID           string `json:"wake_id"`
	TaskID           string `json:"task_id"`
	DeploymentID     string `json:"deployment_id"`
	Fingerprint      string `json:"fingerprint"`
	Output           string `json:"output"`
	Status           string `json:"status"`
	ContractVerified bool   `json:"contract_verified"`
}

func cmdDataAPISync(args []string) int {
	fs := newFlagSet("data-api sync", flag.ContinueOnError)
	configPath := fs.String("config", "", "JSON workflow file with output, migrate, optional permissions and check commands")
	timeout := fs.Duration("timeout", 20*time.Minute, "deadline for the entire workflow (maximum 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *configPath == "" || *timeout <= 0 || *timeout > time.Hour {
		PrintUsage(os.Stderr, "usage: gregale data-api sync NAME --config FILE [--timeout DURATION]", "data-api")
		return 1
	}
	config, err := loadDataAPISyncConfig(*configPath)
	if err != nil {
		return printErr("Invalid Data API workflow", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	interrupted, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupted, *timeout)
	defer cancel()
	receipt, err := runDataAPISync(ctx, client, fs.Arg(0), config)
	if err != nil {
		return printErr("Data API sync failed", dataAPISyncDiagnostic(err, receipt))
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	PrintOK(osStdout, "Migrations, schema refresh, types and client check completed for %s; types written to %s", receipt.App, receipt.Output)
	return 0
}

func loadDataAPISyncConfig(path string) (dataAPISyncConfig, error) {
	var config dataAPISyncConfig
	file, err := openCustomerFile(path)
	if err != nil {
		return config, err
	}
	defer func() { _ = file.Close() }()
	const maxConfigBytes = 64 << 10
	content, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return config, err
	}
	if len(content) > maxConfigBytes {
		return config, errors.New("workflow file exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("read workflow JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return config, errors.New("workflow file must contain one JSON object")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return config, err
	}
	if config.Output == "" {
		return config, errors.New("output is required")
	}
	config.Output = dataAPISyncPath(base, config.Output)
	parent, err := os.Stat(filepath.Dir(config.Output))
	if err != nil || !parent.IsDir() {
		return config, errors.New("output parent must be an existing directory")
	}
	if current, err := os.Lstat(config.Output); err == nil && !current.Mode().IsRegular() {
		return config, errors.New("output must be a regular file or a new file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return config, fmt.Errorf("inspect output: %w", err)
	}
	for _, step := range []struct {
		name    string
		command *dataAPISyncCommand
	}{{"migrate", &config.Migrate}, {"check", &config.Check}} {
		if err := prepareDataAPISyncCommand(base, step.command); err != nil {
			return config, fmt.Errorf("%s: %w", step.name, err)
		}
	}
	if config.Permissions != nil {
		if err := prepareDataAPISyncCommand(base, config.Permissions); err != nil {
			return config, fmt.Errorf("permissions: %w", err)
		}
	}
	if config.AccessTokenEnv == "" {
		config.AccessTokenEnv = "GREGALE_DATA_API_ACCESS_TOKEN"
	}
	if !dataAPITokenEnvName.MatchString(config.AccessTokenEnv) {
		return config, errors.New("access_token_env must be an environment variable name")
	}
	config.accessToken = os.Getenv(config.AccessTokenEnv)
	if !validDataAPIAccessToken(config.accessToken) {
		return config, errors.New("configure a nonempty application JWT in access_token_env (default GREGALE_DATA_API_ACCESS_TOKEN); do not use an account key")
	}
	return config, nil
}

func dataAPISyncPath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func prepareDataAPISyncCommand(base string, step *dataAPISyncCommand) error {
	if len(step.Command) == 0 || strings.TrimSpace(step.Command[0]) == "" {
		return errors.New("command must be a nonempty argument array")
	}
	for _, arg := range step.Command {
		if strings.ContainsRune(arg, 0) {
			return errors.New("command arguments must not contain NUL")
		}
	}
	step.Directory = dataAPISyncPath(base, step.Directory)
	dir, err := os.Stat(step.Directory)
	if err != nil || !dir.IsDir() {
		return errors.New("directory must exist")
	}
	executable := step.Command[0]
	if strings.ContainsAny(executable, `/\`) && !filepath.IsAbs(executable) {
		executable = dataAPISyncPath(step.Directory, executable)
	}
	step.Command[0], err = exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("find command executable: %w", err)
	}
	return nil
}

func runDataAPISync(ctx context.Context, client *api.Client, slug string, config dataAPISyncConfig) (dataAPISyncReceipt, error) {
	receipt := dataAPISyncReceipt{App: slug, Output: config.Output}
	// Resolve the public URL before running a migration. Management reads are
	// safe here; no restart or type task is submitted until migration succeeds.
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return receipt, fmt.Errorf("inspect app: %w", err)
	}
	healthURL, err := dataAPIReadinessURL(app)
	if err != nil {
		return receipt, err
	}
	dataAPISyncProgress("Running migration command")
	if err = runDataAPISyncCommand(ctx, config.Migrate); err != nil {
		return receipt, fmt.Errorf("migration command: %w", err)
	}
	if config.Permissions != nil {
		dataAPISyncProgress("Running RPC permission setup")
		if err = runDataAPISyncCommand(ctx, *config.Permissions); err != nil {
			return receipt, fmt.Errorf("RPC permission setup: %w", err)
		}
	}
	dataAPISyncProgress("Refreshing the Data API and waiting for readiness")
	restart, err := client.RestartAppFresh(ctx, slug)
	receipt.WakeID = restart.WakeID
	if err != nil {
		return receipt, fmt.Errorf("request schema refresh: %w", err)
	}
	if err = waitDataAPIRefresh(ctx, client, slug, receipt.WakeID, healthURL, time.Second); err != nil {
		return receipt, err
	}
	dataAPISyncProgress("Generating database types")
	task, err := generateDataAPITypes(ctx, client, slug)
	receipt.TaskID, receipt.DeploymentID = task.ID, task.DeploymentID
	if err != nil {
		return receipt, fmt.Errorf("generate types: %w", err)
	}
	receipt.Fingerprint = dataAPIFingerprint.FindStringSubmatch(task.StdoutTail)[1]
	dataAPISyncProgress("Verifying the serving schema contract")
	if err = verifyDataAPIServingContract(ctx, healthURL, config.accessToken, receipt.Fingerprint); err != nil {
		return receipt, fmt.Errorf("verify serving contract (existing types retained): %w", err)
	}
	receipt.ContractVerified = true
	if err = writeDataAPITypes(config.Output, false, task.StdoutTail); err != nil {
		return receipt, fmt.Errorf("write types: %w", err)
	}
	dataAPISyncProgress("Running client check")
	if err = runDataAPISyncCommand(ctx, config.Check); err != nil {
		return receipt, fmt.Errorf("client check (generated types retained): %w", err)
	}
	receipt.Status = "completed"
	return receipt, nil
}

func dataAPISyncProgress(message string) {
	if !jsonOutput {
		PrintProgress(osStderr, "%s", message)
	}
}

func runDataAPISyncCommand(ctx context.Context, step dataAPISyncCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, step.Command[0], step.Command[1:]...) //nolint:gosec // Explicit developer argv from the requested workflow, without a shell.
	command.Dir, command.Stdin = step.Directory, os.Stdin
	// Keep stdout exclusively for the final receipt, including nested CLI output.
	command.Stdout, command.Stderr = osStderr, osStderr
	command.WaitDelay = 2 * time.Second
	configureDataAPISyncProcess(command)
	defer cleanupDataAPISyncProcess(command)
	err := command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func dataAPISyncDiagnostic(err error, receipt dataAPISyncReceipt) error {
	code, title := "data_api_sync_failed", "Data API sync failed"
	status := http.StatusBadRequest
	if errors.Is(err, context.DeadlineExceeded) {
		code, title = "data_api_sync_timeout", "Data API sync timed out"
		status = http.StatusRequestTimeout
	} else if errors.Is(err, context.Canceled) {
		code, title = "data_api_sync_cancelled", "Data API sync stopped"
		status = http.StatusRequestTimeout
	}
	detail := err.Error()
	if receipt.WakeID != "" {
		detail += fmt.Sprintf(" (wake_id=%s)", receipt.WakeID)
	}
	if receipt.TaskID != "" {
		detail += fmt.Sprintf(" (task_id=%s)", receipt.TaskID)
	}
	detail += "; completed steps are not rolled back, and accepted remote work is not cancelled"
	return &APIError{Problem: api.Problem{Status: status, Code: code, Title: title, Detail: detail}}
}
