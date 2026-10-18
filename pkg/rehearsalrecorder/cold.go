package rehearsalrecorder

import (
	"context"
	"errors"
	"sync"
)

type ColdStore interface {
	ScenarioColdBootRequired(context.Context, Identity) (bool, error)
	RecordScenarioColdBoot(context.Context, Identity, string, string, int64) error
	FenceScenarioColdRetirement(context.Context, Identity, string, string) error
	CompleteScenarioColdRetirement(context.Context, Identity, string, string) error
	FailScenarioColdPreparation(context.Context, string) error
}

// RecordBoot is called by the original boot owner after actual guest readiness.
// A command VM cannot supply serving-app lifecycle evidence.
func (s *Session) RecordBoot(ctx context.Context, method string, elapsedMS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.command {
		return nil
	}
	store, ok := s.store.(ColdStore)
	if !ok {
		return nil
	}
	if s.failed || s.closing {
		return errors.New("cold-start boot owner is unavailable")
	}
	return store.RecordScenarioColdBoot(ctx, s.identity, s.id, method, elapsedMS)
}

func (s *Session) PrepareColdRetirement(ctx context.Context, preparation string) error {
	s.mu.Lock()
	store, ok := s.store.(ColdStore)
	if !ok || preparation == "" || s.command || s.failed || s.closing || !s.active || s.workerFaultID != "" {
		s.mu.Unlock()
		return errors.New("original cold-preparation recorder is unavailable")
	}
	s.retiring, s.closing = true, true
	stops := make([]func(), 0, len(s.connections))
	for _, stop := range s.connections {
		stops = append(stops, stop)
	}
	s.mu.Unlock()
	if err := store.FenceScenarioColdRetirement(ctx, s.identity, preparation, s.id); err != nil {
		s.Fail()
		return err
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
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || len(s.connections) != 0 {
		return errors.New("cold-preparation collectors did not settle")
	}
	return nil
}

func (s *Session) FinishColdRetirement(ctx context.Context, preparation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, ok := s.store.(ColdStore)
	if !ok || !s.retiring || s.failed || len(s.connections) != 0 {
		return errors.New("cold-preparation retirement cannot be acknowledged")
	}
	if err := store.CompleteScenarioColdRetirement(ctx, s.identity, preparation, s.id); err != nil {
		return err
	}
	s.retiring, s.retired = false, true
	s.cancel()
	return nil
}
