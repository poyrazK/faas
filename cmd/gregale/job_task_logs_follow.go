package main

// Live job task output (ADR-1163): the CLI side of
// GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/onebox-faas/faas/pkg/api"
)

// jobTaskLogFollow reports what a live stream delivered.
type jobTaskLogFollow struct {
	// Lines counts relayed output lines.
	Lines int
	// Reason is the server's end reason; empty when the stream broke.
	Reason string
	// Gaps counts evidence of missing output, including unresolved gap frames.
	Gaps int
	// AttemptChanges counts changes of VM across a disconnected stream.
	AttemptChanges int
	// UnverifiedResume means a reopened stream ended before identifying its VM.
	UnverifiedResume bool
	replay           jobTaskLogReplay
	localErr         error
}

// Complete reports whether every line the task wrote was relayed.
func (f jobTaskLogFollow) Complete() bool {
	return f.Reason == api.JobTaskLogsEndFinished && f.Gaps == 0 && f.AttemptChanges == 0 && !f.UnverifiedResume
}

// followJobTaskLogs relays one task's output until the server ends the
// stream. Lines tagged stderr go to stderr. An error before any frame means
// the stream could not be used at all.
func followJobTaskLogs(ctx context.Context, client *api.Client, job, runID string, taskIndex int, stdout, stderr io.Writer) (jobTaskLogFollow, error) {
	return followJobTaskLogsWithRetry(ctx, client, job, runID, taskIndex, stdout, stderr, jobTaskLogReconnectDelays())
}

func readJobTaskLogStream(ctx context.Context, client *api.Client, job, runID string, taskIndex int, stdout, stderr io.Writer, res *jobTaskLogFollow) error {
	// A numeric cursor belongs to the VM that emitted it. Replaying the
	// retained window and deduplicating by instance is safe across attempts
	// and also works with gateways that cannot bind a cursor to a VM.
	body, err := client.StreamJobTaskLogs(ctx, job, runID, taskIndex, 0)
	if err != nil {
		return err
	}
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() {
		_ = dec.Close()
		// Closing the reader also releases a parser blocked on its buffered
		// event channel. Do not render or advance cursors while draining.
		for range dec.Events() {
		}
	}()
	for {
		select {
		case ev, ok := <-dec.Events():
			if !ok {
				return streamEndError(dec)
			}
			if done := res.apply(ev, stdout, stderr); done {
				return res.localErr
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// apply handles one frame and reports whether it ended the stream.
func (f *jobTaskLogFollow) apply(ev api.Event, stdout, stderr io.Writer) bool {
	switch ev.Event {
	case "log":
		var line api.LogEvent
		if json.Unmarshal([]byte(ev.Data), &line) != nil {
			f.Gaps++
			f.replay.unsafe = true
			return false
		}
		return f.applyLogLine(line, stdout, stderr)
	case "gap":
		var gap api.LogGapEvent
		if f.replay.opening && json.Unmarshal([]byte(ev.Data), &gap) == nil && gap.Reason == "seq_below_retained" {
			f.replay.gapPending = true
		} else {
			f.Gaps++
		}
	case "end":
		var end struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(ev.Data), &end) != nil {
			f.localErr = errors.New("invalid task log end frame")
			return true
		}
		f.Reason = end.Reason
		return true
	}
	return false
}

// splitJobLogFollow removes --follow / -f from jobs logs arguments.
func splitJobLogFollow(args []string) ([]string, bool) {
	remaining := make([]string, 0, len(args))
	follow := false
	for _, arg := range args {
		if arg == "--follow" || arg == "-follow" || arg == "-f" {
			follow = true
			continue
		}
		remaining = append(remaining, arg)
	}
	return remaining, follow
}

// followJobTaskLogsCommand runs `gregale jobs logs --follow`.
func followJobTaskLogsCommand(client *api.Client, job, runID string, taskIndex int) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	live, err := followJobTaskLogs(ctx, client, job, runID, taskIndex, osStdout, osStderr)
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return 130
	case err != nil && live.Lines == 0 && live.Reason == "":
		return printErr("Could not follow the task output", err)
	case live.Lines == 0 && live.Reason == api.JobTaskLogsEndStoredOutput:
		logs, err := client.GetJobTaskLogs(ctx, job, runID, taskIndex)
		if err != nil {
			return printErr("Logs request failed", err)
		}
		return renderJobTaskLogs(logs)
	case !live.Complete():
		PrintWarn(osStderr, "live output was incomplete (%s); stored output: %s jobs logs %s %s %d",
			liveIncompleteReason(live, err), recoveryCLI(), recoveryArgument(job), runID, taskIndex)
	}
	return 0
}

func streamEndError(dec *api.Decoder) error {
	if err, ok := <-dec.Errors(); ok && err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, bufio.ErrTooLong) {
			return err
		}
		return &jobTaskLogReadError{err}
	}
	return fmt.Errorf("the log stream closed before the task finished: %w", io.ErrUnexpectedEOF)
}
