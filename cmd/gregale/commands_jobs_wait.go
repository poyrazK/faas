package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdJobsWait(args []string) int {
	fs := newFlagSet("jobs wait", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose a Job and run")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum wait duration")
	interval := fs.Duration("poll-interval", 2*time.Second, "polling interval")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if *timeout <= 0 || *interval <= 0 || *interactive && fs.NArg() != 0 || !*interactive && fs.NArg() != 2 {
		return printErr("Invalid wait arguments", errors.New("use jobs wait JOB RUN_ID or jobs wait --interactive, with positive --timeout and --poll-interval"))
	}
	if *interactive && (jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY()) {
		return printErr("Interactive terminal required", errors.New("use jobs wait JOB RUN_ID for scripts"))
	}
	if !*interactive && (!jobSlugPattern.MatchString(fs.Arg(0)) || !jobRunIDPattern.MatchString(fs.Arg(1))) {
		return printErr("Invalid wait target", errors.New("provide a valid Job name and run UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var job api.JobResponse
	var run api.JobRunResponse
	if *interactive {
		prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
		var selected bool
		job, run, selected, err = chooseJobRun(signalCtx, client, prompt)
		if err != nil {
			return jobLogPickerError(err)
		}
		if !selected {
			return 0
		}
	} else {
		readCtx, cancel := context.WithTimeout(signalCtx, 30*time.Second)
		job, err = client.GetJob(readCtx, fs.Arg(0))
		cancel()
		if err != nil {
			return printErr("Could not read Job", err)
		}
		if job.Name != fs.Arg(0) {
			return printErr("Invalid Job", errors.New("server returned another Job"))
		}
		run.ID = fs.Arg(1)
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "jobs", "wait", quoteLogCommandArg(job.Name), quoteLogCommandArg(run.ID), "--timeout", timeout.String(), "--poll-interval", interval.String())
	resume := strings.Join(command, " ")
	if !jsonOutput {
		_, _ = fmt.Fprintln(osStdout, "Resume command (POSIX shells):\n"+resume)
	}
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	result, code, waitErr := followJobRun(ctx, client, job, run.ID, *interval)
	if jsonOutput {
		receipt := struct {
			Run           *api.JobRunResponse `json:"run,omitempty"`
			ExitCode      int                 `json:"exit_code"`
			ResumeCommand string              `json:"resume_command"`
			Error         string              `json:"error,omitempty"`
		}{ExitCode: code, ResumeCommand: resume}
		if result.ID != "" {
			receipt.Run = &result
		}
		if waitErr != nil {
			receipt.Error = waitErr.Error()
		}
		if outputCode := jsonOut(writeJSONSingle(receipt)); outputCode != 0 {
			return outputCode
		}
	} else if waitErr != nil {
		printErr("Job wait ended", waitErr)
	}
	return code
}

func followJobRun(ctx context.Context, client *api.Client, job api.JobResponse, id string, interval time.Duration) (api.JobRunResponse, int, error) {
	var last api.JobRunResponse
	previous := ""
	for {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		got, err := client.GetJobRun(readCtx, job.Name, id)
		cancel()
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return last, 130, ctx.Err()
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return last, 124, ctx.Err()
			}
			return last, 1, err
		}
		if got.ID != id || got.JobID != job.ID || got.AccountID != job.AccountID {
			return last, 1, errors.New("server returned another run or Job")
		}
		last = got
		progress := fmt.Sprintf("Run %s: %s; running=%d succeeded=%d failed=%d cancelled=%d total=%d", id, oneLine(got.AggregateStatus), got.TasksRunning, got.TasksSucceeded, got.TasksFailed, got.TasksCancelled, got.Tasks)
		if !jsonOutput && progress != previous {
			PrintProgress(osStdout, "%s", progress)
			previous = progress
		}
		switch got.AggregateStatus {
		case "succeeded":
			return last, 0, nil
		case "failed", "cancelled", "dead_letter":
			return last, 1, nil
		case "queued", "running":
		default:
			return last, 1, errors.New("unknown Job run status")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.Canceled) {
				return last, 130, ctx.Err()
			}
			return last, 124, ctx.Err()
		case <-timer.C:
		}
	}
}
