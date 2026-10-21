package rehearsalrecorder

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

var ErrSuspended = errors.New("rehearsal recorder is suspended")

type SuspendStore interface {
	AdvanceScenarioSuspendResume(context.Context, Identity, string, string, string, string, int64, int64) (rehearsalresume.Spec, error)
}

// PrepareSuspend retains the original owner and fences all new requests before
// joining its relay parsers. Unlike retirement, the session is never closed.
func (s *Session) PrepareSuspend(ctx context.Context, receipt string) (rehearsalresume.Spec, error) {
	var empty rehearsalresume.Spec
	s.mu.Lock()
	store, ok := s.store.(SuspendStore)
	if !ok || receipt == "" || s.command || !s.active || s.failed || s.closing || s.workerFaultID != "" || s.suspendID != "" {
		s.mu.Unlock()
		return empty, errors.New("original suspend/resume recorder unavailable")
	}
	s.suspendID, s.suspended = receipt, true
	stops := make([]func(), 0, len(s.connections))
	for connection, stop := range s.connections {
		if connection == s.checkpointConnection && s.checkpointID == receipt {
			continue
		}
		stops = append(stops, stop)
	}
	s.mu.Unlock()
	spec, err := store.AdvanceScenarioSuspendResume(ctx, s.identity, s.id, receipt, "claimed", "fenced", 0, 0)
	if err != nil {
		s.Fail()
		return empty, err
	}
	joined := make(chan struct{})
	go func() {
		var group sync.WaitGroup
		for _, stop := range stops {
			group.Go(stop)
		}
		group.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-ctx.Done():
		s.Fail()
		return empty, ctx.Err()
	}
	// Shutdown joins HTTP handlers; the Serve owner's deferred Detach can run
	// just after it returns. Wait for that original owner, without adopting it.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		failed, closing, pending := s.failed, s.closing, len(s.connections)
		held := s.checkpointID == receipt && s.checkpointConnection != nil
		if held {
			if _, found := s.connections[s.checkpointConnection]; found {
				pending--
			} else {
				failed = true
			}
		}
		s.mu.Unlock()
		if failed || closing {
			return empty, errors.New("suspend/resume collectors did not settle")
		}
		if pending == 0 {
			if spec.ManagedJob() != held {
				s.Fail()
				return empty, errors.New("suspend/resume application checkpoint is not held")
			}
			return spec, nil
		}
		select {
		case <-ctx.Done():
			s.Fail()
			return empty, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Session) advanceSuspend(ctx context.Context, receipt, expected, next string, pause, resume int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, ok := s.store.(SuspendStore)
	if !ok || s.suspendID != receipt || s.failed || s.closing || !s.active {
		return errors.New("original suspend/resume recorder lost")
	}
	if s.checkpointID != "" && (s.checkpointID != receipt || s.checkpointContext == nil || s.checkpointContext.Err() != nil) {
		return errors.New("original running-job checkpoint disconnected")
	}
	_, err := store.AdvanceScenarioSuspendResume(ctx, s.identity, s.id, receipt, expected, next, pause, resume)
	if err == nil && next == "resuming" {
		s.suspended = false
	}
	if err == nil && next == "resumed" {
		if s.checkpointID == receipt {
			s.checkpointResumed = true
		} else {
			s.suspendID = ""
		}
	}
	return err
}
func (s *Session) RecordSuspended(ctx context.Context, receipt string) error {
	return s.advanceSuspend(ctx, receipt, "fenced", "paused", 0, 0)
}
func (s *Session) BeginResume(ctx context.Context, receipt string, pauseMS int64) error {
	return s.advanceSuspend(ctx, receipt, "paused", "resuming", pauseMS, 0)
}
func (s *Session) RecordResumed(ctx context.Context, receipt string, resumeMS int64) error {
	return s.advanceSuspend(ctx, receipt, "resuming", "resumed", 0, resumeMS)
}
