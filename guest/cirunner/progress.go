package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func (t *Transport) progress(ctx context.Context, update ci.ProgressUpdate) error {
	raw, err := json.Marshal(update)
	if err != nil || len(raw) > api.CIJobsProgressRequestMaxBytes {
		return errTransfer
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIJobsProgressSeconds)*time.Second)
	defer cancel()
	for range api.CIJobsTransferMaxAttempts {
		request, err := t.request(ctx, http.MethodPut, "progress", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := t.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return errTransfer
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, api.CIJobsProgressRequestMaxBytes+1))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK {
			var receipt ci.ProgressReceipt
			if readErr == nil && len(body) <= api.CIJobsProgressRequestMaxBytes && json.Unmarshal(body, &receipt) == nil && receipt.Sequence == update.Sequence && receipt.NextOffset == update.LogOffset+len(update.Logs) {
				return nil
			}
			continue
		}
		if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			return errTransfer
		}
	}
	return errTransfer
}

// One sender owns sequence/ack state. Commands only replace bounded snapshots;
// telemetry I/O never holds the log writer or changes command success/failure.
type liveProgress struct {
	mu        sync.Mutex
	steps     []ci.StepResult
	logs      *prefixLog
	transport *Transport
	wake      chan struct{}
	done      chan struct{}
	stop      context.CancelFunc
	sequence  int32
	offset    int
	lastSteps []ci.StepResult
	truncated bool
	pending   *ci.ProgressUpdate
}

func startLiveProgress(ctx context.Context, transport *Transport, job ci.PlannedJob, logs *prefixLog) *liveProgress {
	ctx, cancel := context.WithCancel(ctx)
	p := &liveProgress{transport: transport, logs: logs, wake: make(chan struct{}, 1), done: make(chan struct{}), stop: cancel}
	for _, step := range job.Steps {
		p.steps = append(p.steps, ci.StepResult{Name: step.Name, Status: ci.StatusQueued})
	}
	p.lastSteps = cloneProgressSteps(p.steps)
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(time.Duration(api.CIJobsProgressPollMillis) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			case <-ticker.C:
			}
			_ = p.flush(ctx)
		}
	}()
	return p
}

func cloneProgressSteps(steps []ci.StepResult) []ci.StepResult {
	return append([]ci.StepResult(nil), steps...)
}

func (p *liveProgress) set(index int, step ci.StepResult) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.steps[index] = step
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *liveProgress) flush(ctx context.Context) error {
	for p.sequence < api.CIJobsProgressMaxUpdates {
		if p.pending == nil {
			p.mu.Lock()
			steps := cloneProgressSteps(p.steps)
			p.mu.Unlock()
			logs, truncated := p.logs.snapshot()
			if p.offset == len(logs) && p.truncated == truncated && reflect.DeepEqual(steps, p.lastSteps) {
				return nil
			}
			end := min(len(logs), p.offset+api.CILogPageMaxBytes)
			p.pending = &ci.ProgressUpdate{Sequence: p.sequence + 1, LogOffset: p.offset, Logs: bytes.Clone(logs[p.offset:end]), Steps: steps, LogTruncated: truncated && end == api.CILogMaxBytesPerAttempt}
		}
		if err := p.transport.progress(ctx, *p.pending); err != nil {
			return err
		}
		p.sequence, p.offset = p.pending.Sequence, p.pending.LogOffset+len(p.pending.Logs)
		p.lastSteps, p.truncated = p.pending.Steps, p.pending.LogTruncated
		p.pending = nil
	}
	return nil
}

func (p *liveProgress) finish(ctx context.Context, steps []ci.StepResult) {
	if p == nil {
		return
	}
	p.stop()
	<-p.done
	p.mu.Lock()
	p.steps = cloneProgressSteps(steps)
	p.mu.Unlock()
	_ = p.flush(ctx)
}
