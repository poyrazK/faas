// adr: 1359
package rehearsalrecorder

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

type SuspendCheckpointStore interface {
	SuspendStore
	ScenarioSuspendResume(context.Context, string) (rehearsalresume.Receipt, error)
	FailScenarioSuspendResume(context.Context, string) error
}

// HeldSuspendCheckpoint reports only this process's original held response.
// Durable rows alone can never authorize an in-flight exception.
func (s *Session) HeldSuspendCheckpoint(receipt string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, attached := s.connections[s.checkpointConnection]
	return receipt != "" && s.checkpointID == receipt && attached && s.checkpointContext != nil && s.checkpointContext.Err() == nil && !s.failed && !s.closing && s.active
}

// HoldSuspendCheckpoint releases the successful simulated response only after
// physical resume, readiness and scheduler publication of the same VM.
func (s *Session) HoldSuspendCheckpoint(ctx context.Context, receipt string, connection *Connection) (err error) {
	s.mu.Lock()
	store, ok := s.store.(SuspendCheckpointStore)
	_, attached := s.connections[connection]
	if !ok || receipt == "" || connection == nil || !attached || s.command || !s.active || s.failed || s.closing || s.suspendID != "" || s.checkpointID != "" || s.workerFaultID != "" {
		s.mu.Unlock()
		s.Fail()
		return errors.New("original running-job checkpoint owner unavailable")
	}
	s.checkpointID, s.checkpointConnection = receipt, connection
	s.checkpointContext = ctx
	s.mu.Unlock()
	defer func() {
		if err != nil {
			s.Fail()
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), operationTimeout)
			defer cancel()
			_ = store.FailScenarioSuspendResume(cleanup, receipt)
		}
	}()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		_, err = store.AdvanceScenarioSuspendResume(ctx, s.identity, s.id, receipt, "waiting", "armed", 0, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, rehearsalresume.ErrCheckpointBusy) {
			return err
		}
		if !s.HeldSuspendCheckpoint(receipt) {
			return errors.New("running-job checkpoint disconnected while awaiting command retirement")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		held := s.checkpointID == receipt && !s.failed && !s.closing
		resumed := s.checkpointResumed
		s.mu.Unlock()
		if !held {
			return errors.New("running-job checkpoint owner lost")
		}
		r, readErr := store.ScenarioSuspendResume(ctx, receipt)
		if readErr != nil {
			return readErr
		}
		if r.State == "failed" {
			return errors.New("running-job suspend/resume failed")
		}
		if r.State == "complete" {
			if !resumed {
				return errors.New("running-job resume was not observed by its original collector")
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.failed || s.closing || s.checkpointID != receipt {
				return errors.New("running-job checkpoint owner lost before release")
			}
			s.checkpointID, s.checkpointConnection, s.suspendID = "", nil, ""
			s.checkpointContext = nil
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
