package main

// gregale run --image (ADR-1161): run one command from an OCI image in a
// fresh microVM, `docker run` style. Arguments after the flags are the
// command; they replace the image CMD while the image ENTRYPOINT stays,
// unless --entrypoint overrides it.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	imageRunPollInterval = 2 * time.Second
	imageRunPullTimeout  = 10 * time.Minute
	// imageRunAttachWait bounds the wait for an interactive run's VM: queueing
	// behind the plan's concurrency plus boot.
	imageRunAttachWait = 5 * time.Minute
)

// imageRunEnvFlags collects repeatable KEY=VALUE flags.
type imageRunEnvFlags map[string]string

func (f imageRunEnvFlags) String() string { return "" }

func (f imageRunEnvFlags) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return fmt.Errorf("expected KEY=VALUE, got %q", value)
	}
	f[key] = val
	return nil
}

// runImageRequested reports whether `gregale run` was given --image before
// any "--" terminator, which selects image mode and its own flag set.
func runImageRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--image" || arg == "-image" || strings.HasPrefix(arg, "--image=") || strings.HasPrefix(arg, "-image=") {
			return true
		}
	}
	return false
}

type imageRunOptions struct {
	request api.CreateImageRunRequest
	detach  bool
}

func parseImageRunArgs(args []string) (imageRunOptions, bool) {
	fs := newFlagSet("run", flag.ContinueOnError)
	image := fs.String("image", "", "OCI image to run")
	entrypoint := fs.String("entrypoint", "", "override the image ENTRYPOINT")
	env := imageRunEnvFlags{}
	fs.Var(env, "env", "KEY=VALUE environment variable (repeatable)")
	fs.Var(env, "e", "shorthand for --env")
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
	if strings.TrimSpace(*image) == "" || *ram < 0 || *timeout < 0 || *timeout%time.Second != 0 || (tty && !interactive) {
		PrintUsage(osStderr, "usage: gregale run [-i [-t]] --image IMAGE [--entrypoint CMD] [-e KEY=VALUE]... [--ram MB] [--timeout D] [--pull missing|always] [--detach] [ARG...]", "run")
		return imageRunOptions{}, false
	}
	if interactive && (*detach || jsonOutput) {
		PrintFail(osStderr, "--interactive cannot be combined with --detach or --json")
		return imageRunOptions{}, false
	}
	req := api.CreateImageRunRequest{Image: *image, RAMMB: *ram, TimeoutSec: int(*timeout / time.Second), Pull: *pull,
		Interactive: interactive, TTY: tty}
	if len(env) > 0 {
		req.Env = env
	}
	if *entrypoint != "" {
		req.Entrypoint = &[]string{*entrypoint}
	}
	if rest := fs.Args(); len(rest) > 0 {
		req.Args = &rest
	}
	return imageRunOptions{request: req, detach: *detach}, true
}

func cmdRunImage(args []string) int {
	opts, ok := parseImageRunArgs(args)
	if !ok {
		return 1
	}
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
	req.Pull = api.ImageRunPullMissing
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
