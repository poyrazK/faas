package main

// gregale run --image (ADR-1161): run one command from an OCI image in a
// fresh microVM, `docker run` style. Arguments after the flags are the
// command; they replace the image CMD while the image ENTRYPOINT stays,
// unless --entrypoint overrides it. ADR-1790 adds docker's -u, -w,
// --env-file, `-e KEY` pass-through, a positional image and private
// registry credentials.

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	imageRunPollInterval = 2 * time.Second
	imageRunPullTimeout  = 10 * time.Minute
	// imageRunAttachWait bounds the wait for an interactive run's VM: queueing
	// behind the plan's concurrency plus boot.
	imageRunAttachWait = 5 * time.Minute
)

// imageRunEnvPasswordVar supplies the registry password without a flag
// value that would land in shell history.
const imageRunEnvPasswordVar = "GREGALE_REGISTRY_PASSWORD"

// imageRunEnvFileMaxBytes bounds one --env-file.
const imageRunEnvFileMaxBytes = 1 << 20

// imageRunStringList collects a repeatable flag in order.
type imageRunStringList []string

func (f *imageRunStringList) String() string { return "" }

func (f *imageRunStringList) Set(value string) error {
	*f = append(*f, value)
	return nil
}

// disposableRunFlags are the flags only `gregale run`'s disposable source
// runs (ADR-171) take. Without one of them, a positional argument names an
// image, as `docker run IMAGE` does; disposable runs take no positional.
var disposableRunFlags = map[string]bool{
	"runtime": true, "profile": true, "workflow-id": true, "step-label": true, "source": true,
	"file": true, "dir": true, "output-file": true, "artifact-input": true, "output-dir": true,
	"input": true, "timeout-ms": true, "memory-mb": true, "cpu-millicores": true,
	"ephemeral-disk-mb": true, "max-output-bytes": true, "wait": true, "watch": true,
	"poll-interval": true, "wait-timeout": true,
}

// runImageRequested reports whether `gregale run` selects image mode: an
// --image flag, or a positional argument without any disposable-run flag,
// before any "--" terminator.
func runImageRequested(args []string) bool {
	positional := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			positional = true
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "image" {
			return true
		}
		if disposableRunFlags[name] {
			return false
		}
	}
	return positional
}

// imageRunEnv builds the run environment as docker does: --env-file files
// in order, then -e flags, later values winning. KEY without =VALUE passes
// the local value through and is skipped when unset.
func imageRunEnv(files, flags []string, lookup func(string) (string, bool)) (map[string]string, error) {
	env := map[string]string{}
	set := func(entry, where string) error {
		key, val, explicit := strings.Cut(entry, "=")
		switch {
		case key == "":
			return fmt.Errorf("%s: no variable name in %q", where, entry)
		case strings.IndexFunc(key, unicode.IsSpace) >= 0:
			return fmt.Errorf("%s: variable %q contains whitespace", where, key)
		case explicit:
			env[key] = val
		default:
			if local, ok := lookup(key); ok {
				env[key] = local
			}
		}
		return nil
	}
	for _, path := range files {
		entries, err := readImageRunEnvFile(path)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := set(entry.text, fmt.Sprintf("%s:%d", path, entry.line)); err != nil {
				return nil, err
			}
		}
	}
	for _, entry := range flags {
		if err := set(entry, "-e"); err != nil {
			return nil, err
		}
	}
	return env, nil
}

type imageRunEnvLine struct {
	text string
	line int
}

// readImageRunEnvFile reads docker's env-file format: one KEY=VALUE or KEY
// per line, leading whitespace ignored, `#` comments and blank lines
// skipped, values taken literally (no quotes or interpolation).
func readImageRunEnvFile(path string) ([]imageRunEnvLine, error) {
	f, err := os.Open(path) //nolint:gosec,forbidigo // a CLI user's own --env-file
	if err != nil {
		return nil, fmt.Errorf("--env-file: %w", err)
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, imageRunEnvFileMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("--env-file %s: %w", path, err)
	}
	if len(body) > imageRunEnvFileMaxBytes {
		return nil, fmt.Errorf("--env-file %s exceeds %d bytes", path, imageRunEnvFileMaxBytes)
	}
	var out []imageRunEnvLine
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 0, 64*1024), imageRunEnvFileMaxBytes)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimLeftFunc(strings.TrimSuffix(scanner.Text(), "\r"), unicode.IsSpace)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, imageRunEnvLine{text: line, line: n})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("--env-file %s: %w", path, err)
	}
	return out, nil
}

type imageRunOptions struct {
	request api.CreateImageRunRequest
	detach  bool
	// registryUsername and passwordStdin select private registry
	// credentials; the password is read later, never from a flag value.
	registryUsername string
	passwordStdin    bool
}

const imageRunUsage = "usage: gregale run [-i [-t]] [-u USER] [-w DIR] [-e KEY[=VALUE]]... [--env-file FILE]... " +
	"[--registry-username USER [--registry-password-stdin]] [--entrypoint CMD] [--ram MB] [--timeout D] " +
	"[--pull missing|always] [--detach] (--image IMAGE | IMAGE) [ARG...]"

func parseImageRunArgs(args []string) (imageRunOptions, bool) {
	fs := newFlagSet("run", flag.ContinueOnError)
	image := fs.String("image", "", "OCI image to run")
	entrypoint := fs.String("entrypoint", "", "override the image ENTRYPOINT")
	var envFlags, envFiles imageRunStringList
	fs.Var(&envFlags, "env", "KEY=VALUE environment variable, or KEY to pass the local value (repeatable)")
	fs.Var(&envFlags, "e", "shorthand for --env")
	fs.Var(&envFiles, "env-file", "read KEY=VALUE lines from a file (repeatable)")
	user := fs.String("user", "", "run as name|uid[:group|gid] instead of the image USER")
	fs.StringVar(user, "u", "", "shorthand for --user")
	workdir := fs.String("workdir", "", "absolute working directory instead of the image WORKDIR")
	fs.StringVar(workdir, "w", "", "shorthand for --workdir")
	registryUsername := fs.String("registry-username", "", "username for a private registry")
	passwordStdin := fs.Bool("registry-password-stdin", false, "read the registry password or token from stdin")
	ram := fs.Int("ram", 0, "memory in MB (0 = Job default)")
	timeout := fs.Duration("timeout", 0, "task deadline (0 = the plan's Job ceiling)")
	pull := fs.String("pull", api.ImageRunPullMissing, "pull policy: missing|always")
	detach := fs.Bool("detach", false, "queue the run and print its id without waiting")
	fs.BoolVar(detach, "d", false, "shorthand for --detach")
	var interactive, tty, both bool
	fs.BoolVar(&interactive, "interactive", false, "attach stdin and run until the command exits (ADR-1330)")
	fs.BoolVar(&interactive, "i", false, "shorthand for --interactive")
	fs.BoolVar(&tty, "tty", false, "give the command a terminal; requires --interactive")
	fs.BoolVar(&tty, "t", false, "shorthand for --tty")
	fs.BoolVar(&both, "it", false, "shorthand for --interactive --tty")
	fs.BoolVar(&both, "ti", false, "shorthand for --interactive --tty")
	if err := fs.Parse(args); err != nil {
		return imageRunOptions{}, false
	}
	interactive, tty = interactive || both, tty || both
	rest := fs.Args()
	if strings.TrimSpace(*image) == "" && len(rest) > 0 {
		*image, rest = rest[0], rest[1:]
	}
	if strings.TrimSpace(*image) == "" || *ram < 0 || *timeout < 0 || *timeout%time.Second != 0 || (tty && !interactive) ||
		(*passwordStdin && *registryUsername == "") {
		PrintUsage(osStderr, imageRunUsage, "run")
		return imageRunOptions{}, false
	}
	if interactive && (*detach || jsonOutput) {
		PrintFail(osStderr, "--interactive cannot be combined with --detach or --json")
		return imageRunOptions{}, false
	}
	if interactive && *passwordStdin {
		PrintFail(osStderr, "--registry-password-stdin cannot be combined with --interactive: stdin is the session; use "+imageRunEnvPasswordVar)
		return imageRunOptions{}, false
	}
	env, err := imageRunEnv(envFiles, envFlags, os.LookupEnv)
	if err != nil {
		PrintFail(osStderr, "%v", err)
		return imageRunOptions{}, false
	}
	req := api.CreateImageRunRequest{Image: *image, RAMMB: *ram, TimeoutSec: int(*timeout / time.Second), Pull: *pull,
		Interactive: interactive, TTY: tty, User: *user, Workdir: *workdir}
	if len(env) > 0 {
		req.Env = env
	}
	if *entrypoint != "" {
		req.Entrypoint = &[]string{*entrypoint}
	}
	if len(rest) > 0 {
		req.Args = &rest
	}
	return imageRunOptions{request: req, detach: *detach, registryUsername: *registryUsername, passwordStdin: *passwordStdin}, true
}

// imageRunRegistryAuth reads the registry password for --registry-username:
// from stdin with --registry-password-stdin, else from
// $GREGALE_REGISTRY_PASSWORD, else from a terminal prompt.
func imageRunRegistryAuth(opts imageRunOptions) (*api.ImageRunRegistryAuth, error) {
	if opts.registryUsername == "" {
		return nil, nil
	}
	var password string
	switch {
	case opts.passwordStdin:
		body, err := io.ReadAll(io.LimitReader(osStdin, int64(api.MaxRegistryPasswordBytes)+2))
		if err != nil {
			return nil, fmt.Errorf("read registry password: %w", err)
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(body), "\n"), "\r")
	case os.Getenv(imageRunEnvPasswordVar) != "":
		password = os.Getenv(imageRunEnvPasswordVar)
	case stdinIsTTY():
		value, err := readInteractivePassword(bufio.NewReader(osStdin), "Registry password/token: ")
		if err != nil {
			return nil, fmt.Errorf("read registry password: %w", err)
		}
		password = value
	}
	if password == "" {
		return nil, fmt.Errorf("--registry-username needs a password: --registry-password-stdin, %s, or a terminal", imageRunEnvPasswordVar)
	}
	if len(password) > api.MaxRegistryPasswordBytes {
		return nil, fmt.Errorf("registry password exceeds %d bytes", api.MaxRegistryPasswordBytes)
	}
	return &api.ImageRunRegistryAuth{Username: opts.registryUsername, Password: password}, nil
}

func cmdRunImage(args []string) int {
	opts, ok := parseImageRunArgs(args)
	if !ok {
		return 1
	}
	auth, err := imageRunRegistryAuth(opts)
	if err != nil {
		return printErr("Invalid registry credentials", err)
	}
	opts.request.RegistryAuth = auth
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if opts.request.Interactive {
		return runInteractiveImage(ctx, client, opts.request)
	}
	queued, err := startImageRun(ctx, client, opts.request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return 130
		}
		return printErr("Could not start the image run", err)
	}
	if opts.detach {
		return reportDetachedImageRun(queued)
	}
	return waitImageRun(ctx, client, queued)
}

// startImageRun creates the run, waiting while the image is pulled. Only the
// first request carries --pull always; later calls must not re-queue.
func startImageRun(ctx context.Context, client *api.Client, req api.CreateImageRunRequest) (api.ImageRunResponse, error) {
	resp, err := client.CreateImageRun(ctx, req)
	if err != nil || resp.Status == api.ImageRunStatusQueued {
		return resp, err
	}
	if !jsonOutput {
		PrintProgress(osStderr, "Pulling %s", resp.Image)
	}
	// The first request stored any registry credential on the runner job;
	// polls do not resend the password.
	req.Pull, req.RegistryAuth = api.ImageRunPullMissing, nil
	pullCtx, cancel := context.WithTimeout(ctx, imageRunPullTimeout)
	defer cancel()
	lastErr := ""
	for {
		job, err := client.GetJob(pullCtx, resp.Job)
		if err != nil {
			return resp, err
		}
		switch job.ImageMaterializationStatus {
		case "ready":
			resp, err = client.CreateImageRun(ctx, req)
			if err != nil || resp.Status == api.ImageRunStatusQueued {
				return resp, err
			}
		case "failed":
			return resp, fmt.Errorf("pull %s: %s", resp.Image, job.ImageMaterializationError)
		}
		if msg := job.ImageMaterializationError; msg != "" && msg != lastErr && !jsonOutput {
			PrintWarn(osStderr, "retrying pull: %s", msg)
			lastErr = msg
		}
		if err := sleepContext(pullCtx, imageRunPollInterval); err != nil {
			return resp, fmt.Errorf("waiting for %s: %w", resp.Image, err)
		}
	}
}

// runInteractiveImage is `gregale run -it --image` (ADR-1330): create the
// run, attach to its task once the VM is up, and relay the session. The exit
// code is the command's. Leaving before the session starts cancels the run.
func runInteractiveImage(ctx context.Context, client *api.Client, req api.CreateImageRunRequest) int {
	stdinFile, tty := interactiveStdin(req.TTY)
	req.TTY = tty
	queued, err := startImageRun(ctx, client, req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return 130
		}
		return printErr("Could not start the image run", err)
	}
	if queued.Attach == nil || queued.Attach.Token == "" {
		cancelImageRun(ctx, client, queued)
		return printErr("Interactive session unavailable", errors.New("the server did not return attach credentials"))
	}
	waitCtx, cancel := context.WithTimeout(ctx, imageRunAttachWait)
	defer cancel()
	conn, err := dialAttach(waitCtx, client, *queued.Attach, tty, stdinFile)
	if err != nil {
		cancelImageRun(ctx, client, queued)
		if errors.Is(ctx.Err(), context.Canceled) {
			return 130
		}
		return printErr("Could not attach to the session", err)
	}
	defer func() { _ = conn.Close() }()
	return relayAttachedSession(ctx, conn, tty, stdinFile)
}

func reportDetachedImageRun(resp api.ImageRunResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintln(osStdout, resp.RunID)
	_, _ = fmt.Fprintf(osStderr, "Task:   %s jobs tasks %s %s\nOutput: %s jobs logs %s %s\n",
		recoveryCLI(), recoveryArgument(resp.Job), resp.RunID, recoveryCLI(), recoveryArgument(resp.Job), resp.RunID)
	return 0
}

// waitImageRun relays the task's output live (ADR-1163), waits for the run to
// finish and returns the task's exit code. Ctrl-C cancels the run.
func waitImageRun(ctx context.Context, client *api.Client, resp api.ImageRunResponse) int {
	var live jobTaskLogFollow
	var liveErr error
	if !jsonOutput {
		live, liveErr = followJobTaskLogs(ctx, client, resp.Job, resp.RunID, 0, osStdout, osStderr)
		if errors.Is(ctx.Err(), context.Canceled) {
			cancelImageRun(ctx, client, resp)
			return 130
		}
	}
	for {
		run, err := client.GetJobRun(ctx, resp.Job, resp.RunID)
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				cancelImageRun(ctx, client, resp)
				return 130
			}
			return printErr("Could not read the run", err)
		}
		switch run.AggregateStatus {
		case "succeeded", "failed", "cancelled", "dead_letter":
			return finishImageRun(ctx, client, resp, live, liveErr)
		}
		if err := sleepContext(ctx, imageRunPollInterval); err != nil {
			cancelImageRun(ctx, client, resp)
			return 130
		}
	}
}

func cancelImageRun(ctx context.Context, client *api.Client, resp api.ImageRunResponse) {
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := client.CancelJobRun(cancelCtx, resp.Job, resp.RunID); err != nil {
		PrintWarn(osStderr, "could not cancel run %s: %v", resp.RunID, err)
	}
}

type imageRunResult struct {
	Run  api.ImageRunResponse `json:"run"`
	Task api.JobTaskResponse  `json:"task"`
	Log  string               `json:"log"`
	Exit int                  `json:"exit_code"`
}

// finishImageRun reports the outcome. Output already relayed live is not
// printed again; when the live stream was unavailable the stored output is
// printed instead.
func finishImageRun(ctx context.Context, client *api.Client, resp api.ImageRunResponse, live jobTaskLogFollow, liveErr error) int {
	tasks, err := client.ListJobRunTasks(ctx, resp.Job, resp.RunID)
	if err != nil || len(tasks.Tasks) == 0 {
		return printErr("Could not read the task", errors.Join(err, errors.New("run has no task")))
	}
	task := tasks.Tasks[0]
	logs, err := client.GetJobTaskLogsWithMaxBytes(ctx, resp.Job, resp.RunID, task.TaskIndex, api.MaxJobTaskLogMaxBytes)
	if err != nil {
		return printErr("Could not read the output", err)
	}
	result := imageRunResult{Run: resp, Task: task, Log: logs.LogContent, Exit: imageRunExitCode(task)}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
		return result.Exit
	}
	switch {
	case live.Complete():
	case live.Lines == 0:
		_, _ = fmt.Fprint(osStdout, logs.LogContent)
		if logs.Truncated {
			PrintWarn(osStderr, "output truncated to the last %d bytes", logs.MaxBytes)
		}
	default:
		PrintWarn(osStderr, "live output was incomplete (%s); stored output: %s jobs logs %s %s %d",
			liveIncompleteReason(live, liveErr), recoveryCLI(), recoveryArgument(resp.Job), resp.RunID, task.TaskIndex)
	}
	if result.Exit != 0 && task.ErrorMessage != "" {
		PrintWarn(osStderr, "%s", task.ErrorMessage)
	}
	return result.Exit
}

// imageRunExitCode maps the task outcome to a shell exit code: the command's
// own code when it ran, 124 for a deadline, 130 for cancellation.
func imageRunExitCode(task api.JobTaskResponse) int {
	switch {
	case task.Status == "succeeded":
		return 0
	case task.Status == "timeout":
		return 124
	case task.Status == "cancelled":
		return 130
	case task.ExitCode != 0:
		return task.ExitCode
	default:
		return 1
	}
}

// sleepContext waits d or until ctx ends.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func liveIncompleteReason(live jobTaskLogFollow, err error) string {
	switch {
	case live.Gaps > 0:
		return "some lines were dropped before they could be sent"
	case live.Reason == api.JobTaskLogsEndStoredOutput:
		return "the last attempt ended before its output could be streamed"
	case live.Reason != "":
		return live.Reason
	case err != nil:
		return err.Error()
	default:
		return "the stream closed early"
	}
}
