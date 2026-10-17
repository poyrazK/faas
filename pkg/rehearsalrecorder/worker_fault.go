// adr: 1356
package rehearsalrecorder

import (
	"context"
	"errors"
	"sync"
)

type WorkerFaultStore interface {
	ReadyScenarioWorkerCheckpoint(context.Context, Identity, string, string) error
	FenceScenarioWorkerTermination(context.Context, Identity, string, string) error
	CompleteScenarioWorkerTermination(context.Context, Identity, string, string) error
	FailScenarioWorkerFault(context.Context, string) error
}

// HoldWorkerCheckpoint runs in the original host relay after it has received
// the broker's completed simulation receipt. No response is sent to the guest.
func (s *Session) HoldWorkerCheckpoint(ctx context.Context, fault string) error {
	s.mu.Lock()
	store, ok := s.store.(WorkerFaultStore)
	if !ok || fault == "" || s.command || s.failed || s.closing || !s.active || s.workerFaultID != "" {
		s.mu.Unlock()
		s.Fail()
		return errors.New("worker checkpoint owner unavailable")
	}
	s.workerFaultID = fault
	s.workerCheckpointRelease = make(chan struct{})
	release := s.workerCheckpointRelease
	s.mu.Unlock()
	if err := store.ReadyScenarioWorkerCheckpoint(ctx, s.identity, fault, s.id); err != nil {
		s.Fail()
		return err
	}
	select {
	case <-release:
		return ErrClosing
	case <-ctx.Done():
		s.Fail()
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), operationTimeout)
		defer cancel()
		_ = store.FailScenarioWorkerFault(cleanup, fault)
		return ctx.Err()
	}
}

func (s *Session) PrepareWorkerTermination(ctx context.Context, fault string) error {
	s.mu.Lock()
	store, ok := s.store.(WorkerFaultStore)
	if !ok || s.command || s.failed || s.closing || !s.active || s.workerFaultID != fault || s.workerCheckpointRelease == nil {
		s.mu.Unlock()
		return errors.New("original worker checkpoint is not held")
	}
	s.retiring, s.closing = true, true
	stops := make([]func(), 0, len(s.connections))
	for _, stop := range s.connections {
		stops = append(stops, stop)
	}
	s.mu.Unlock()
	if err := store.FenceScenarioWorkerTermination(ctx, s.identity, fault, s.id); err != nil {
		s.Fail()
		return err
	}
	s.mu.Lock()
	close(s.workerCheckpointRelease)
	s.mu.Unlock()
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
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || len(s.connections) != 0 {
		return errors.New("worker collectors did not settle")
	}
	return nil
}

func (s *Session) FinishWorkerTermination(ctx context.Context, fault string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, ok := s.store.(WorkerFaultStore)
	if !ok || s.workerFaultID != fault || !s.retiring || s.failed || len(s.connections) != 0 {
		return errors.New("worker termination cannot be acknowledged")
	}
	if err := store.CompleteScenarioWorkerTermination(ctx, s.identity, fault, s.id); err != nil {
		return err
	}
	s.retiring, s.retired = false, true
	s.cancel()
	return nil
}
