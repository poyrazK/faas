package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// These delays bound reconnects for one follow invocation, including streams
// that made progress. They never resubmit the job or change its retry policy.
func jobTaskLogReconnectDelays() []time.Duration {
	return []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
}

type jobTaskLogReplay struct {
	sequences       map[string]int64
	lastInstance    string
	opening         bool
	gapPending      bool
	unsafe          bool
	requireIdentity bool
}

// Streaming HTTP/2 resets do not necessarily implement net.Error. Once an
// SSE body is open, a read failure is reconnectable except a parser size limit.
type jobTaskLogReadError struct{ error }

func (e *jobTaskLogReadError) Unwrap() error { return e.error }

func followJobTaskLogsWithRetry(ctx context.Context, client *api.Client, job, runID string, taskIndex int, stdout, stderr io.Writer, delays []time.Duration) (live jobTaskLogFollow, err error) {
	defer func() {
		if live.replay.gapPending {
			live.Gaps++ // No following line proved that the gap was already printed.
		}
		live.UnverifiedResume = live.replay.opening && live.Lines > 0
	}()
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return live, ctx.Err()
		}
		if attempt > 0 {
			if live.replay.unsafe || attempt > len(delays) {
				return live, err
			}
			if pauseErr := waitJobTaskLogReconnect(ctx, delays[attempt-1]); pauseErr != nil {
				return live, pauseErr
			}
			live.replay.opening = true
			live.replay.requireIdentity = live.Lines > 0
		}
		live.Reason = ""
		err = readJobTaskLogStream(ctx, client, job, runID, taskIndex, stdout, stderr, &live)
		if ctx.Err() != nil {
			return live, ctx.Err()
		}
		if live.localErr != nil || !retryJobTaskLogStream(live.Reason, err) {
			return live, err
		}
	}
}

func waitJobTaskLogReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryJobTaskLogStream(reason string, err error) bool {
	if err == nil {
		return reason == api.JobTaskLogsEndTimeout || reason == api.JobTaskLogsEndUnreachable
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var readErr *jobTaskLogReadError
	if errors.As(err, &readErr) {
		return true
	}
	// A URL wrapper also covers certificate and protocol failures. Only its
	// underlying network error is reconnectable; HTTP refusals stay final.
	var requestErr *url.Error
	if errors.As(err, &requestErr) {
		err = requestErr.Err
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func (f *jobTaskLogFollow) applyLogLine(line api.LogEvent, stdout, stderr io.Writer) bool {
	identified := line.InstanceID != "" && line.Seq > 0
	if !identified && f.replay.requireIdentity {
		f.localErr = errors.New("resumed task log stream cannot identify its output")
		return true
	}
	previous := f.replay.sequences[line.InstanceID]
	if f.replay.opening {
		if identified && f.replay.lastInstance != "" && f.replay.lastInstance != line.InstanceID {
			f.AttemptChanges++
		}
		// A replay can report that old, already printed lines expired. It
		// lost new output only if its first retained line is beyond our cursor.
		if f.replay.gapPending && (!identified || previous == 0 || line.Seq > previous && line.Seq-previous > 1) {
			f.Gaps++
		}
		f.replay.opening = false
		f.replay.gapPending = false
	}
	if identified && line.Seq <= previous {
		return false
	}
	if identified && line.Seq > previous && line.Seq-previous > 1 {
		f.Gaps++
	}
	w := stdout
	if line.Stream == "stderr" {
		w = stderr
	}
	n, err := fmt.Fprintln(w, line.Line)
	if err == nil && n != len(line.Line)+1 {
		err = io.ErrShortWrite
	}
	if err != nil {
		f.localErr = fmt.Errorf("write task log output: %w", err)
		return true
	}
	f.Lines++
	if !identified {
		// Older streams may omit identity. Preserve their output, but do
		// not reconnect after printing it: deduplication cannot be proven.
		f.replay.unsafe = true
		return false
	}
	if f.replay.sequences == nil {
		f.replay.sequences = make(map[string]int64)
	}
	f.replay.sequences[line.InstanceID] = line.Seq
	f.replay.lastInstance = line.InstanceID
	return false
}
