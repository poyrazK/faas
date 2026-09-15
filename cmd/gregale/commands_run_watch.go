package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	executionWatchRetryInitial = 100 * time.Millisecond
	executionWatchRetryMax     = 2 * time.Second
)

// executionWatchEvent is the stable line-oriented shape emitted by
// `gregale run --watch --json`. Data preserves the public SSE payload while
// the optional receipt gives agents the complete terminal result in the same
// frame as the terminal event.
type executionWatchEvent struct {
	ID      string                 `json:"id,omitempty"`
	Type    string                 `json:"type"`
	Data    json.RawMessage        `json:"data,omitempty"`
	Receipt *api.ExecutionResponse `json:"receipt,omitempty"`
}

// watchExecution follows the resumable execution event stream until the
// terminal frame. A disconnect is safe to retry because the last SSE id is
// sent back as the `after` cursor; output is therefore never replayed twice.
func watchExecution(ctx context.Context, client *api.Client, executionID string) (api.ExecutionResponse, error) {
	var (
		lastID    int64
		connected bool
		retry     = executionWatchRetryInitial
	)
	for {
		body, err := client.StreamExecution(ctx, executionID, lastID)
		if err != nil {
			if !connected || !executionWatchRetryable(err) {
				return api.ExecutionResponse{}, err
			}
			if !waitExecutionWatchRetry(ctx, &retry) {
				return api.ExecutionResponse{}, ctx.Err()
			}
			continue
		}
		connected = true
		retry = executionWatchRetryInitial
		terminal, terminalEvent, observedID, streamErr := consumeExecutionWatchStream(ctx, body)
		_ = body.Close()
		if observedID > lastID {
			lastID = observedID
		}
		if terminal {
			receipt, getErr := client.GetExecution(ctx, executionID)
			if getErr != nil {
				return api.ExecutionResponse{}, fmt.Errorf("load terminal run receipt: %w", getErr)
			}
			if err := emitExecutionWatchEvent(terminalEvent, &receipt); err != nil {
				return api.ExecutionResponse{}, err
			}
			return receipt, nil
		}
		if ctx.Err() != nil {
			return api.ExecutionResponse{}, ctx.Err()
		}
		if streamErr != nil && !executionWatchRetryable(streamErr) {
			return api.ExecutionResponse{}, streamErr
		}
		if !jsonOutput {
			if streamErr != nil {
				PrintWarn(osStderr, "run event stream interrupted: %v; reconnecting", streamErr)
			} else {
				PrintWarn(osStderr, "run event stream ended; reconnecting")
			}
		}
		if !waitExecutionWatchRetry(ctx, &retry) {
			return api.ExecutionResponse{}, ctx.Err()
		}
	}
}

func consumeExecutionWatchStream(ctx context.Context, body io.ReadCloser) (bool, api.Event, int64, error) {
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	var lastID int64
	for {
		select {
		case <-ctx.Done():
			return false, api.Event{}, lastID, ctx.Err()
		case event, ok := <-dec.Events():
			if !ok {
				streamErr := <-dec.Errors()
				if errors.Is(streamErr, io.EOF) {
					streamErr = nil
				}
				return false, api.Event{}, lastID, streamErr
			}
			if id, err := strconv.ParseInt(event.ID, 10, 64); err == nil && id > lastID {
				lastID = id
			}
			if event.Event == "terminal" {
				return true, event, lastID, nil
			}
			if err := emitExecutionWatchEvent(event, nil); err != nil {
				return false, api.Event{}, lastID, err
			}
			if event.Event == "error" {
				return false, api.Event{}, lastID, fmt.Errorf("execution event stream: %s", event.Data)
			}
		}
	}
}

func emitExecutionWatchEvent(event api.Event, receipt *api.ExecutionResponse) error {
	if jsonOutput {
		data := json.RawMessage(event.Data)
		if len(data) == 0 {
			data = nil
		} else if !json.Valid(data) {
			var err error
			data, err = json.Marshal(event.Data)
			if err != nil {
				return err
			}
		}
		return json.NewEncoder(osStdout).Encode(executionWatchEvent{
			ID: event.ID, Type: event.Event, Data: data, Receipt: receipt,
		})
	}
	switch event.Event {
	case "status":
		var payload struct {
			Status api.ExecutionStatus `json:"status"`
		}
		if json.Unmarshal([]byte(event.Data), &payload) == nil && payload.Status != "" {
			PrintProgress(osStdout, "Run status=%s.", payload.Status)
		}
	case "stdout", "stderr":
		var payload struct {
			Chunk string `json:"chunk"`
		}
		if json.Unmarshal([]byte(event.Data), &payload) != nil {
			return fmt.Errorf("invalid %s execution event", event.Event)
		}
		if event.Event == "stderr" {
			_, _ = fmt.Fprint(osStderr, payload.Chunk)
		} else {
			_, _ = fmt.Fprint(osStdout, payload.Chunk)
		}
	}
	return nil
}

func executionWatchRetryable(err error) bool {
	if err == nil {
		return true
	}
	if problem := api.AsProblem(err); problem != nil {
		return problem.Status == 408 || problem.Status == 429 || problem.Status >= 500
	}
	return true
}

func waitExecutionWatchRetry(ctx context.Context, retry *time.Duration) bool {
	timer := time.NewTimer(*retry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		if *retry < executionWatchRetryMax {
			*retry *= 2
			if *retry > executionWatchRetryMax {
				*retry = executionWatchRetryMax
			}
		}
		return true
	}
}
