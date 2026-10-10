package main

// Developer database seeding for `gregale dev --postgres`.
//
// The CLI never receives database credentials, so it cannot connect to the
// developer database itself. The seed command instead runs inside the
// developer app through the existing deployment-attached app task path
// (ADR-230), which already loads the app's scoped env, secrets, and managed
// bindings — including DATABASE_URL. No new privileged path is introduced.
//
// A seed runs once per provisioned database. The marker is local because a
// developer database is owned by exactly one local workspace (its name is the
// workspace-scoped developer app slug), and it is keyed by the database ID so
// a database recreated after --stop or lease expiry is seeded again.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	devSeedPollInterval   = 2 * time.Second
	devSeedBindingTimeout = 3 * time.Minute
	devSeedTaskTimeout    = 15 * time.Minute
	devSeedBindingReady   = "ready"
)

var devSeedDatabaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// devSeedOps isolates the API calls so the seed runner is unit-testable.
type devSeedOps struct {
	getBinding     func(context.Context, string) (api.ManagedPostgresBinding, error)
	createTask     func(context.Context, string, api.CreateAppTaskRequest) (api.AppTaskResponse, error)
	getTask        func(context.Context, string, string) (api.AppTaskResponse, error)
	pollInterval   time.Duration
	bindingTimeout time.Duration
	taskTimeout    time.Duration
}

func clientDevSeedOps(client *Client) devSeedOps {
	return devSeedOps{
		getBinding:     client.GetManagedPostgresBinding,
		createTask:     client.CreateAppTask,
		getTask:        client.GetAppTask,
		pollInterval:   devSeedPollInterval,
		bindingTimeout: devSeedBindingTimeout,
		taskTimeout:    devSeedTaskTimeout,
	}
}

// devSeedEvent is the machine-readable seed receipt. It never contains the
// command output, connection details, or secret values.
type devSeedEvent struct {
	Event      string `json:"event"`
	Database   string `json:"database"`
	DatabaseID string `json:"database_id"`
	TaskID     string `json:"task_id,omitempty"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

func validateDevSeedCommand(command string) error {
	if _, problem := (api.CreateAppTaskRequest{Command: []string{command}, CommandShell: true}).Resolve(); problem != nil {
		return errors.New(problem.Detail)
	}
	return nil
}

func devSeedMarkerPath(databaseID string) (string, error) {
	if !devSeedDatabaseIDPattern.MatchString(databaseID) {
		return "", fmt.Errorf("invalid developer database ID %q", databaseID)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gregale", "dev-postgres-seeds", databaseID), nil
}

func devSeedCompleted(databaseID string) (bool, error) {
	path, err := devSeedMarkerPath(databaseID)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

// recordDevSeedCompleted stores a digest of the command, never the command
// itself, so the marker cannot echo anything sensitive typed into a flag.
func recordDevSeedCompleted(databaseID, command, taskID string) error {
	path, err := devSeedMarkerPath(databaseID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(command))
	body := fmt.Sprintf("task=%s\ncommand_sha256=%s\nseeded_at=%s\n", taskID, hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339))
	file, err := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

// shouldRunDevSeed reports whether a configured seed is still due for the
// session's current database.
func shouldRunDevSeed(command string, postgres *api.DevPostgresResponse, reseed bool) (bool, error) {
	if command == "" || postgres == nil || postgres.DatabaseID == "" {
		return false, nil
	}
	if reseed {
		return true, nil
	}
	done, err := devSeedCompleted(postgres.DatabaseID)
	if err != nil {
		return false, err
	}
	return !done, nil
}

// waitForDevSeedBinding blocks until DATABASE_URL is bound. Seeding before the
// binding is ready would run the command without the variable it needs.
func waitForDevSeedBinding(ctx context.Context, ops devSeedOps, postgres *api.DevPostgresResponse) error {
	if postgres.BindingState == devSeedBindingReady {
		return nil
	}
	if postgres.BindingID == "" {
		return errors.New("developer database binding is not available yet")
	}
	waitCtx, cancel := context.WithTimeout(ctx, ops.bindingTimeout)
	defer cancel()
	for {
		binding, err := ops.getBinding(waitCtx, postgres.BindingID)
		if err == nil {
			switch binding.State {
			case devSeedBindingReady:
				return nil
			case "failed", "deleting", "deleted", "retiring":
				if binding.LastErrorCode != "" {
					return fmt.Errorf("developer database binding is %s (%s)", binding.State, binding.LastErrorCode)
				}
				return fmt.Errorf("developer database binding is %s", binding.State)
			}
		} else if waitCtx.Err() == nil {
			return fmt.Errorf("read developer database binding: %w", err)
		}
		select {
		case <-waitCtx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return ctx.Err()
			}
			return fmt.Errorf("developer database binding was not ready within %s", ops.bindingTimeout)
		case <-time.After(ops.pollInterval):
		}
	}
}

// runDevPostgresSeed runs the seed command as an app task against the live
// developer deployment and waits for its terminal result.
func runDevPostgresSeed(ctx context.Context, ops devSeedOps, slug string, postgres *api.DevPostgresResponse, command string) (api.AppTaskResponse, error) {
	if err := waitForDevSeedBinding(ctx, ops, postgres); err != nil {
		return api.AppTaskResponse{}, err
	}
	task, err := ops.createTask(ctx, slug, api.CreateAppTaskRequest{Command: []string{command}, CommandShell: true})
	if err != nil {
		return api.AppTaskResponse{}, fmt.Errorf("start database seed: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, ops.taskTimeout)
	defer cancel()
	for !task.Status.Terminal() {
		select {
		case <-waitCtx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return task, ctx.Err()
			}
			return task, fmt.Errorf("database seed task %s is still running after %s; the seed will be retried after the next live sync", task.ID, ops.taskTimeout)
		case <-time.After(ops.pollInterval):
		}
		next, getErr := ops.getTask(waitCtx, slug, task.ID)
		if getErr != nil {
			if waitCtx.Err() != nil {
				continue
			}
			return task, fmt.Errorf("read database seed task %s: %w", task.ID, getErr)
		}
		task = next
	}
	if task.Status != api.AppTaskStatusSucceeded {
		return task, devSeedTaskError(task)
	}
	return task, nil
}

func devSeedTaskError(task api.AppTaskResponse) error {
	detail := fmt.Sprintf("database seed task %s finished with status=%s", task.ID, task.Status)
	if task.ExitCode != nil {
		detail += fmt.Sprintf(" exit_code=%d", *task.ExitCode)
	}
	if task.Failure != nil {
		detail += fmt.Sprintf(" (%s: %s)", task.Failure.Code, task.Failure.Message)
	}
	return errors.New(detail)
}

func devSeedDiagnostic(err error) devDiagnostic {
	p := &api.Problem{Code: devDiagSeedFailed}
	decorateDevDiagnosticProblem(p, devDiagSeedFailed)
	d := devDiagnostic{
		Event:  "developer_diagnostic",
		Code:   devDiagSeedFailed,
		Title:  p.Title,
		Detail: errString(err),
		Phase:  "seed",
		Hint:   p.Hint,
		Why:    p.Why,
		Fix:    p.Fix,
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Detail != "" {
		d.Detail = apiErr.Problem.Detail
	}
	return d
}

// renderDevSeedOutput prints the bounded task output tails with a stable
// prefix so seed output stays distinguishable from build and runtime lines.
func renderDevSeedOutput(task api.AppTaskResponse) {
	for _, line := range strings.Split(strings.TrimRight(task.StdoutTail, "\n"), "\n") {
		if line != "" {
			_, _ = fmt.Fprintf(osStdout, "seed | %s\n", line)
		}
	}
	for _, line := range strings.Split(strings.TrimRight(task.StderrTail, "\n"), "\n") {
		if line != "" {
			_, _ = fmt.Fprintf(osStderr, "seed | %s\n", line)
		}
	}
	if task.OutputTruncated {
		PrintWarn(osStderr, "seed output was truncated at %d bytes", task.MaxOutputBytes)
	}
}

func devSeedEventFor(postgres *api.DevPostgresResponse, task api.AppTaskResponse, status string) devSeedEvent {
	return devSeedEvent{
		Event:      "developer_seed",
		Database:   postgres.Name,
		DatabaseID: postgres.DatabaseID,
		TaskID:     task.ID,
		Status:     status,
		ExitCode:   task.ExitCode,
	}
}

type devSeedOutcome int

const (
	devSeedOutcomeDone devSeedOutcome = iota
	devSeedOutcomeFailed
)

// runDevSeedAfterLiveSync seeds the session database when the seed is still
// due. A failed seed never fails the live sync in watch mode: the source is
// live, and the next successful sync retries the seed.
func runDevSeedAfterLiveSync(ctx context.Context, client *Client, session api.DevSessionResponse, command string, reseed bool, report func(devDiagnostic)) devSeedOutcome {
	return runDevSeedAfterLiveSyncWith(ctx, clientDevSeedOps(client), session, command, reseed, report)
}

func runDevSeedAfterLiveSyncWith(ctx context.Context, ops devSeedOps, session api.DevSessionResponse, command string, reseed bool, report func(devDiagnostic)) devSeedOutcome {
	postgres := session.Postgres
	due, err := shouldRunDevSeed(command, postgres, reseed)
	if err != nil {
		report(devSeedDiagnostic(fmt.Errorf("read local seed marker: %w", err)))
		return devSeedOutcomeFailed
	}
	if !due {
		if postgres != nil && !jsonOutput {
			PrintProgress(osStdout, "PostgreSQL %s already seeded (use --reseed to run the seed again)", postgres.Name)
		}
		return devSeedOutcomeDone
	}
	if !jsonOutput {
		PrintProgress(osStdout, "seeding PostgreSQL %s (waiting for %s, then running the seed in the developer app)", postgres.Name, postgres.EnvironmentKey)
	}
	task, err := runDevPostgresSeed(ctx, ops, session.App.Slug, postgres, command)
	if !jsonOutput && task.ID != "" {
		renderDevSeedOutput(task)
	}
	if err != nil {
		if jsonOutput {
			_ = writeJSON(devSeedEventFor(postgres, task, "failed"))
		}
		report(devSeedDiagnostic(err))
		return devSeedOutcomeFailed
	}
	markerErr := recordDevSeedCompleted(postgres.DatabaseID, command, task.ID)
	if jsonOutput {
		_ = writeJSON(devSeedEventFor(postgres, task, "succeeded"))
	} else {
		PrintOK(osStdout, "PostgreSQL %s seeded (task %s).", postgres.Name, task.ID)
	}
	if markerErr != nil {
		PrintWarn(osStderr, "could not record the completed seed locally; it may run again next time (%v)", markerErr)
	}
	return devSeedOutcomeDone
}
