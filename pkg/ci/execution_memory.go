package ci

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *MemoryStore) executionNow() time.Time {
	if s.executionClock != nil {
		return s.executionClock().UTC()
	}
	return time.Now().UTC()
}

func (s *MemoryStore) SubmitQualification(ctx context.Context, account, key string, req SubmitRequest) (Execution, bool, error) {
	if err := ctx.Err(); err != nil {
		return Execution{}, false, err
	}
	if err := validateSubmission(key, req); err != nil {
		return Execution{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.executionKeys[account+"/"+key]; ok {
		row := s.executions[id].Execution
		if row.Mode != ExecutionQualification || row.RevisionID != req.RevisionID || row.CommitSHA != req.CommitSHA {
			return Execution{}, false, ErrConflict
		}
		return clone(row), true, nil
	}
	revision, ok := s.revisions[req.RevisionID]
	if !ok || revision.AccountID != account {
		return Execution{}, false, ErrNotFound
	}
	count := 0
	for _, row := range s.executions {
		if row.Execution.AccountID == account {
			count++
		}
	}
	if count >= api.CIMaxQualificationExecutionsPerAccount {
		return Execution{}, false, &LimitError{Resource: "qualification_executions", Limit: api.CIMaxQualificationExecutionsPerAccount, Observed: count + 1}
	}
	row, err := newExecution(revision, req, s.executionNow())
	if err != nil {
		return Execution{}, false, err
	}
	s.executions[row.ID] = executionRecord{Execution: row, Version: 1}
	s.executionKeys[account+"/"+key] = row.ID
	return clone(row), false, nil
}

func (s *MemoryStore) Execution(ctx context.Context, account, id string) (Execution, error) {
	if err := ctx.Err(); err != nil {
		return Execution{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.executions[id]
	if !ok || row.Execution.AccountID != account {
		return Execution{}, ErrNotFound
	}
	return clone(row.Execution), nil
}

func (s *MemoryStore) mutateExecution(ctx context.Context, account, id string, mutate func(*executionRecord, time.Time) error) (executionRecord, error) {
	if err := ctx.Err(); err != nil {
		return executionRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.executions[id]
	if !ok || row.Execution.AccountID != account {
		return executionRecord{}, ErrNotFound
	}
	row = clone(row)
	if err := mutate(&row, s.executionNow()); err != nil {
		return executionRecord{}, err
	}
	if _, err := executionJSON(row.Execution); err != nil {
		return executionRecord{}, err
	}
	s.executions[id] = row
	return clone(row), nil
}

func (s *MemoryStore) CancelExecution(ctx context.Context, account, id string) (Execution, error) {
	row, err := s.mutateExecution(ctx, account, id, func(row *executionRecord, now time.Time) error {
		cancelExecutionRecord(row, now)
		return nil
	})
	return row.Execution, err
}

func (s *MemoryStore) claimExecution(ctx context.Context, account, id string) (executionLease, error) {
	row, err := s.mutateExecution(ctx, account, id, claimExecutionRecord)
	return executionLease{executionRecord: row}, err
}

func (s *MemoryStore) checkExecutionLease(ctx context.Context, lease executionLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.executions[lease.Execution.ID]
	if !ok || row.Execution.AccountID != lease.Execution.AccountID {
		return ErrNotFound
	}
	if !validLease(row, lease, s.executionNow()) {
		return ErrLeaseLost
	}
	return nil
}

func (s *MemoryStore) finishExecutionTick(ctx context.Context, lease executionLease, observations []attemptObservation) (Execution, error) {
	row, err := s.mutateExecution(ctx, lease.Execution.AccountID, lease.Execution.ID, func(row *executionRecord, now time.Time) error {
		return finishExecutionRecord(row, lease, observations, now)
	})
	return row.Execution, err
}

var _ ExecutionStore = (*MemoryStore)(nil)
