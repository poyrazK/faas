package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

type testCommandExecutionSpec struct {
	StartupWithin string            `yaml:"startup_within,omitempty" json:"startup_within,omitempty"`
	Version       int               `yaml:"version" json:"version"`
	Runtime       string            `yaml:"runtime" json:"runtime"`
	Env           map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type testContainedCommandReceipt struct {
	Stage           string            `json:"stage"`
	TaskID          string            `json:"task_id"`
	DeploymentID    string            `json:"deployment_id"`
	SpecSHA256      string            `json:"spec_sha256"`
	ReceiptSHA256   string            `json:"receipt_sha256,omitempty"`
	Status          api.AppTaskStatus `json:"status"`
	ExitCode        *int              `json:"exit_code,omitempty"`
	OutputTruncated bool              `json:"output_truncated,omitempty"`
	Stdout          []byte            `json:"stdout,omitempty"`
	Stderr          []byte            `json:"stderr,omitempty"`
}

func validateTestCommandExecution(s testScenario, engine, profile string) error {
	if err := validateTestSuspendResume(s, engine, profile); err != nil {
		return err
	}
	if err := validateTestManagedWork(s); err != nil {
		return err
	}
	if err := validateTestObjects(s); err != nil {
		return err
	}
	if err := validateTestPostgres(s); err != nil {
		return err
	}
	if s.CommandExecution == nil {
		return nil
	}
	if s.ManagedWork != nil && s.ManagedWork.PostgresFault != nil && profile != "warm" {
		return errors.New("postgres_fault requires the real-vm warm profile")
	}
	c := s.CommandExecution
	if c.Version != rehearsalcommand.Version || c.Runtime != "contained" {
		return errors.New("command_execution requires version 1 and runtime contained")
	}
	if engine != "real-vm" || (profile != "warm" && profile != "cold") {
		return errors.New("contained commands require the real-vm engine and warm or cold profile")
	}
	if c.StartupWithin != "" {
		d, err := time.ParseDuration(c.StartupWithin)
		if err != nil || d.Milliseconds() < 1 || d > 2*time.Minute {
			return errors.New("command_execution.startup_within must be between 1ms and 2m")
		}
	}
	if profile == "cold" && s.ManagedWork != nil && s.ManagedWork.WorkerTermination != nil {
		return errors.New("cold-start rehearsals cannot combine worker termination")
	}
	if s.Actions == nil || s.Actions.Network != "contained" || s.Actions.Simulation == nil || s.Behavior == nil {
		return errors.New("contained commands require contained action simulation and declared behavior fixtures and observers")
	}
	if s.Resilience != nil || s.Chaos != nil || s.Load != nil || len(s.Steps) > 0 || len(s.TCPPorts) > 0 || len(s.Buckets) > 0 || s.Postgres || len(s.Consumers) > 0 || s.ConsumerAuthMode != "" {
		return errors.New("contained commands do not yet support resilience, staged steps, load, TCP, Postgres, buckets or consumer credentials")
	}
	if len(s.Requests)+len(s.Checks) == 0 {
		return errors.New("contained commands require an HTTP request or check to verify serving-app lifecycle evidence")
	}
	for _, service := range s.Services {
		if service.Fixture != "" || service.Postgres || len(service.TCPPorts) > 0 {
			return errors.New("contained commands currently support declared HTTP services without Postgres or fixture credentials")
		}
	}
	probe := rehearsalcommand.Spec{Version: 1, RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", Command: []string{"true"}, Env: c.Env}
	if len(c.Env)+5+2*len(s.Services) > 32 {
		return errors.New("contained command environment exceeds 32 entries")
	}
	if err := probe.Validate(); err != nil {
		return err
	}
	for key := range c.Env {
		if strings.HasPrefix(strings.ToUpper(key), "GREGALE_TEST_") {
			return errors.New("command_execution.env cannot override generated GREGALE_TEST variables")
		}
	}
	return nil
}

func prepareTestCommandFiles(sourceDir string, s testScenario) (map[string][]byte, error) {
	if s.CommandExecution == nil {
		return nil, nil
	}
	files := make(map[string][]byte)
	// OpenRoot prevents an input escaping the source even if it changes between
	// hashing and opening. Behavior finalization also rechecks declared hashes.
	root, err := os.OpenRoot(sourceDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	total := 0
	for _, list := range [][]string{s.Behavior.Fixtures, s.Behavior.Observers} {
		for _, name := range list {
			if !rehearsalcommand.ValidPath(name) {
				return nil, errors.New("invalid contained command input path")
			}
			parts := strings.Split(name, "/")
			for i := range parts {
				prefix := strings.Join(parts[:i+1], "/")
				info, err := root.Lstat(prefix)
				if err != nil {
					return nil, err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return nil, errors.New("contained command inputs cannot traverse symlinks")
				}
			}
			f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return nil, err
			}
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() {
				_ = f.Close()
				return nil, errors.New("contained command inputs must be regular files")
			}
			raw, err := io.ReadAll(io.LimitReader(f, rehearsalcommand.MaxInputBytes+1))
			_ = f.Close()
			if err != nil {
				return nil, err
			}
			if _, exists := files[name]; !exists {
				total += len(raw)
			}
			if total > rehearsalcommand.MaxInputBytes {
				return nil, errors.New("contained command inputs exceed 8 KiB")
			}
			files[name] = raw
		}
	}
	if len(files) > rehearsalcommand.MaxFiles {
		return nil, errors.New("too many contained command input files")
	}
	dummy := &testRunReceipt{RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", AppSlug: strings.Repeat("x", 40), Profile: "warm"}
	runner := &testContainedCommandRunner{scenario: s, receipt: dummy, files: files, serviceSlugs: make(map[string]string)}
	for name := range s.Services {
		runner.serviceSlugs[name] = strings.Repeat("x", 40)
	}
	commands := append([][]string{}, s.Setup...)
	commands = append(commands, s.Trigger, s.Command)
	commands = append(commands, s.Cleanup...)
	for _, argv := range commands {
		if len(argv) == 0 {
			continue
		}
		if _, err := rehearsalcommand.Encode(rehearsalcommand.Spec{Version: 1, RunID: dummy.RunID, DeploymentID: dummy.DeploymentID, Command: argv, Env: runner.environment(), Files: files}); err != nil {
			return nil, err
		}
	}
	return files, nil
}

type testContainedCommandClient interface {
	CreateAppTask(context.Context, string, api.CreateAppTaskRequest) (api.AppTaskResponse, error)
	GetAppTask(context.Context, string, string) (api.AppTaskResponse, error)
	CancelAppTask(context.Context, string, string) (api.AppTaskResponse, error)
}

type testContainedCommandRunner struct {
	client       testContainedCommandClient
	scenario     testScenario
	receipt      *testRunReceipt
	files        map[string][]byte
	serviceSlugs map[string]string
}

func (r *testContainedCommandRunner) run(ctx context.Context, stage string, hostEnv, argv []string) error {
	env := r.environment()
	spec := rehearsalcommand.Spec{Version: 1, RunID: r.receipt.RunID, DeploymentID: r.receipt.DeploymentID, Command: argv, Env: env, Files: r.files}
	command, err := rehearsalcommand.Encode(spec)
	if err != nil {
		return err
	}
	seconds := 600
	if deadline, ok := ctx.Deadline(); ok {
		seconds = min(3600, int(time.Until(deadline).Seconds()))
		if seconds < 1 {
			return context.DeadlineExceeded
		}
	}
	row, err := r.client.CreateAppTask(ctx, r.receipt.AppSlug, api.CreateAppTaskRequest{Command: command, TimeoutSeconds: seconds, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes})
	if err != nil {
		return fmt.Errorf("admit contained command: %w", err)
	}
	return r.await(ctx, stage, hostEnv, row, spec, api.AppTaskKindManual)
}

// await follows an admitted contained task to its terminal receipt and
// records it as stage evidence.
func (r *testContainedCommandRunner) await(ctx context.Context, stage string, hostEnv []string, row api.AppTaskResponse, spec rehearsalcommand.Spec, kind api.AppTaskKind) error {
	var err error
	evidence := testContainedCommandReceipt{Stage: stage, TaskID: row.ID, DeploymentID: row.DeploymentID, SpecSHA256: spec.SHA256(), Status: row.Status}
	defer func() { r.receipt.Commands = append(r.receipt.Commands, evidence) }()
	terminal := false
	defer func() {
		if !terminal {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = r.client.CancelAppTask(cleanup, r.receipt.AppSlug, row.ID)
		}
	}()
	for {
		if row.ID != evidence.TaskID || row.DeploymentID != spec.DeploymentID || row.AppID != r.receipt.WorkloadAppIDs[r.scenario.Project] || row.CommandShell || row.Kind != kind || row.MaxOutputBytes != rehearsalcommand.MaxReceiptBytes {
			return errors.New("contained task admission identity differs")
		}
		pinned, err := rehearsalcommand.Decode(row.Command)
		if err != nil || pinned.SHA256() != spec.SHA256() {
			return errors.New("contained task intent differs")
		}
		evidence.Status, evidence.ExitCode = row.Status, row.ExitCode
		switch row.Status {
		case api.AppTaskStatusSucceeded, api.AppTaskStatusFailed, api.AppTaskStatusTimedOut, api.AppTaskStatusCancelled:
			terminal = true
		}
		if terminal {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		row, err = r.client.GetAppTask(ctx, r.receipt.AppSlug, row.ID)
		if err != nil {
			return fmt.Errorf("read contained command receipt: %w", err)
		}
	}
	if row.OutputTruncated {
		return errors.New("contained command receipt was truncated")
	}
	result, err := rehearsalcommand.ParseReceipt([]byte(row.StdoutTail), row.ID, spec)
	if err != nil {
		return fmt.Errorf("validate contained command receipt: %w", err)
	}
	sum := sha256.Sum256([]byte(row.StdoutTail))
	evidence.ReceiptSHA256 = hex.EncodeToString(sum[:])
	evidence.Stdout, evidence.Stderr = result.Stdout, result.Stderr
	evidence.OutputTruncated = result.OutputTruncated
	if stage != "cleanup" {
		for _, pair := range []struct {
			key  string
			data []byte
		}{{"GREGALE_TEST_BEHAVIOR_OUTPUT", result.Behavior}, {"GREGALE_TEST_TRIGGER_OUTPUT", result.Trigger}} {
			if pair.data == nil {
				continue
			}
			value := ""
			for _, entry := range hostEnv {
				key, v, ok := strings.Cut(entry, "=")
				if ok && key == pair.key {
					value = v
				}
			}
			if value != "" {
				if err := writeTestContainedArtifact(value, pair.data); err != nil {
					return err
				}
			}
		}
	}
	if row.Status != api.AppTaskStatusSucceeded || row.ExitCode == nil || *row.ExitCode != 0 || row.Failure != nil || result.OutputTruncated {
		return errors.New("contained command failed or its output was truncated")
	}
	return nil
}

// testTriggerCron is a command cron the recipe fires once, as a contained
// command, on the run's primary app (ADR-20261022194510).
type testTriggerCron struct {
	Command        []string `yaml:"command" json:"command"`
	Shell          bool     `yaml:"shell,omitempty" json:"shell,omitempty"`
	TimeoutSeconds int      `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
}

func validateTestTriggerCron(s testScenario) error {
	c := s.TriggerCron
	if c == nil {
		return nil
	}
	if s.CommandExecution == nil {
		return errors.New("trigger_cron requires command_execution")
	}
	if len(c.Command) == 0 || c.Command[0] == "" || rehearsalcommand.ReleaseArgv(c.Command, c.Shell) == nil {
		return errors.New("trigger_cron needs a command; a shell command is one script string")
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > 3600 {
		return errors.New("trigger_cron timeout_seconds must be between 0 and 3600")
	}
	return nil
}

// testContainedCronClient creates, fires and removes a command cron.
type testContainedCronClient interface {
	CreateCron(context.Context, string, api.CreateCronRequest) (api.CronResponse, error)
	FireCron(context.Context, string) (api.FireCronResponse, error)
	GetFireCronRequest(context.Context, string) (api.FireCronRequestResponse, error)
	DeleteCron(context.Context, string) error
}

// runCron creates the recipe's command cron on the run's app, fires it now
// and follows the task the platform pins as the run's contained command: the
// cron's own argv, with no env, files or retries.
func (r *testContainedCommandRunner) runCron(ctx context.Context, hostEnv []string, c testTriggerCron) error {
	client, ok := r.client.(testContainedCronClient)
	if !ok {
		return errors.New("contained cron runner unavailable")
	}
	spec := rehearsalcommand.Spec{Version: 1, RunID: r.receipt.RunID, DeploymentID: r.receipt.DeploymentID, Command: rehearsalcommand.ReleaseArgv(c.Command, c.Shell)}
	enabled := true
	cron, err := client.CreateCron(ctx, r.receipt.AppSlug, api.CreateCronRequest{AppID: r.receipt.WorkloadAppIDs[r.scenario.Project], Schedule: "0 0 1 1 *",
		Command: c.Command, CommandShell: c.Shell, TimeoutSeconds: c.TimeoutSeconds, Enabled: &enabled})
	if err != nil {
		return fmt.Errorf("create contained cron: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = client.DeleteCron(cleanup, cron.ID)
	}()
	fired, err := client.FireCron(ctx, cron.ID)
	if err != nil {
		return fmt.Errorf("fire contained cron: %w", err)
	}
	for {
		request, err := client.GetFireCronRequest(ctx, fired.RequestID)
		if err != nil {
			return fmt.Errorf("read contained cron fire-now: %w", err)
		}
		switch request.Status {
		case "succeeded":
			if request.TaskID == nil {
				return errors.New("contained cron fire-now created no task")
			}
			row, err := r.client.GetAppTask(ctx, r.receipt.AppSlug, *request.TaskID)
			if err != nil {
				return fmt.Errorf("read contained cron task: %w", err)
			}
			return r.await(ctx, "cron", hostEnv, row, spec, api.AppTaskKindCron)
		case "failed", "cancelled":
			reason := request.Status
			if request.Error != nil {
				reason += ": " + *request.Error
			}
			return fmt.Errorf("contained cron fire-now %s", reason)
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func writeTestContainedArtifact(name string, raw []byte) error {
	if !filepath.IsAbs(name) {
		return errors.New("invalid private command output path")
	}
	root, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(filepath.Base(name), os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("private command output is not a regular file")
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.Write(raw)
	return err
}

func (r *testContainedCommandRunner) environment() map[string]string {
	env := make(map[string]string)
	for key, value := range r.scenario.CommandExecution.Env {
		env[key] = value
	}
	env["GREGALE_TEST_URL"] = "http://" + r.scenario.Project + ".svc.gregale:10081"
	env["GREGALE_TEST_APP_SLUG"], env["GREGALE_TEST_RUN_ID"] = r.receipt.AppSlug, r.receipt.RunID
	env["GREGALE_TEST_PROFILE"], env["GREGALE_TEST_ENGINE"] = r.receipt.Profile, "real-vm"
	for name := range r.scenario.Services {
		key := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		env["GREGALE_TEST_SERVICE_"+key+"_URL"] = "http://" + name + ".svc.gregale:10081"
		env["GREGALE_TEST_SERVICE_"+key+"_APP_SLUG"] = r.serviceSlugs[name]
	}

	return env
}
