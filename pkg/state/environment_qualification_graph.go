package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Read the complete persisted cohort under its original source fence. A caller
// cannot assemble a smaller graph that silently omits a reviewed dependency.
type EnvironmentQualificationGraphStore interface {
	EnvironmentQualificationGraphRequests(context.Context, EnvironmentWorkloadQualificationRequest) ([]EnvironmentWorkloadQualificationRequest, error)
}

func (m *MemStore) EnvironmentQualificationGraphRequests(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest) ([]EnvironmentWorkloadQualificationRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return nil, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) || m.qualificationCurrentLocked(memory, current) != nil {
		return nil, ErrConflict
	}
	var result []EnvironmentWorkloadQualificationRequest
	for _, request := range memory.qualifications {
		if request.GraphID == current.GraphID {
			result = append(result, cloneQualificationRequest(request))
		}
	}
	slices.SortFunc(result, func(a, b EnvironmentWorkloadQualificationRequest) int { return compareQualificationResources(a, b) })
	return result, nil
}

func compareQualificationResources(a, b EnvironmentWorkloadQualificationRequest) int {
	if a.Resource < b.Resource {
		return -1
	}
	if a.Resource > b.Resource {
		return 1
	}
	return 0
}

func (s *PgStore) EnvironmentQualificationGraphRequests(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest) ([]EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationRecoveryUUIDValid(claimed.ID) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.qualificationCurrentTx(ctx, tx, claimed.ID)
	if err != nil {
		return nil, err
	}
	if !qualificationLeaseMatches(qualificationRequestFromSQL(row), claimed, time.Now()) {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().EnvironmentWorkloadQualificationsByGraph(ctx, tx, row.GraphID)
	if err != nil {
		return nil, mapErr(err)
	}
	result := make([]EnvironmentWorkloadQualificationRequest, 0, len(rows))
	for _, request := range rows {
		result = append(result, qualificationRequestFromSQL(request))
	}
	return result, mapErr(tx.Commit(ctx))
}
