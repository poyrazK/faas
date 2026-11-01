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
	if !validOveragePending(pending) {
		return false, ErrInvalid
	}
	step := pending.Step
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryOverageKey{pending.Provider, step.SubjectID, step.PeriodStart.UTC()}
	recorded := false
	if s.overageDeliveries[key].Millicents == step.DeliveredMillicents {
		s.overageDeliveries[key] = OverageDelivery{Provider: pending.Provider, SubjectID: step.SubjectID, PeriodStart: step.PeriodStart.UTC(),
			Live: pending.Live, BilledUnitSeconds: step.BilledUnitSeconds, Millicents: step.TargetMillicents, UpdatedAt: step.At.UTC()}
		recorded = true
	}
	if held, ok := s.overagePending[key]; ok && held.Step.TargetMillicents == step.TargetMillicents {
		delete(s.overagePending, key)
	}
	return recorded, nil
}
