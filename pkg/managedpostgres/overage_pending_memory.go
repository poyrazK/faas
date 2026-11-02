package managedpostgres

import (
	"context"
	"time"
)

// Reserved overage steps (ADR-20261101143527), in memory.

func (s *MemoryStore) OveragePending(_ context.Context, provider, subjectID string, periodStart time.Time) (OveragePendingStep, bool, error) {
	if provider == "" || subjectID == "" || periodStart.IsZero() {
		return OveragePendingStep{}, false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.overagePending[memoryOverageKey{provider, subjectID, monthStart(periodStart)}]
	return pending, ok, nil
}

func (s *MemoryStore) ReserveOverage(_ context.Context, pending OveragePendingStep) (bool, error) {
	if !validOveragePending(pending) {
		return false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryOverageKey{pending.Provider, pending.Step.SubjectID, pending.Step.PeriodStart.UTC()}
	if _, held := s.overagePending[key]; held {
		return false, nil
	}
	pending.Step.PeriodStart, pending.Step.At = pending.Step.PeriodStart.UTC(), pending.Step.At.UTC()
	s.overagePending[key] = pending
	return true, nil
}

func (s *MemoryStore) CompleteOverage(_ context.Context, pending OveragePendingStep) (bool, error) {
	delivery := pendingDelivery(pending)
	if !validOveragePending(pending) || !validOverageDelivery(delivery) || !validOverageAdvance(delivery, pending.Previous) {
		return false, ErrInvalid
	}
	step := pending.Step
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryOverageKey{pending.Provider, step.SubjectID, step.PeriodStart.UTC()}
	recorded := false
	// The row must still be the one the step was computed from, as
	// RecordOverageDelivery requires.
	if current := s.overageDeliveries[key]; current.Millicents == pending.Previous.Millicents &&
		(current.Millicents == 0 || current.Live == pending.Previous.Live) {
		s.overageDeliveries[key] = delivery
		recorded = true
	}
	if held, ok := s.overagePending[key]; ok && held.Step.TargetMillicents == step.TargetMillicents {
		delete(s.overagePending, key)
	}
	return recorded, nil
}
