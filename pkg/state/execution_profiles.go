package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ExecutionRuntimeSelectionStore fences image selection with the active lease.
// A requeued restore must reuse its pinned digest or fail before dispatch.
type ExecutionRuntimeSelectionStore interface {
	PinExecutionRuntime(context.Context, string, string, string, time.Time) (Execution, error)
}

func validExecutionImageDigest(digest string) bool {
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	for _, c := range digest[7:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (m *MemStore) PinExecutionRuntime(_ context.Context, id, lease, digest string, at time.Time) (Execution, error) {
	if !validExecutionImageDigest(digest) || at.IsZero() {
		return Execution{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[id]
	if !ok || row.Status != api.ExecutionStatusRestoring || row.LeaseToken == nil || *row.LeaseToken != lease || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(at) || !row.DeadlineAt.After(at) || row.CancelRequested != nil || (row.RuntimeImageDigest != "" && row.RuntimeImageDigest != digest) {
		return Execution{}, ErrExecutionLeaseLost
	}
	row.RuntimeImageDigest, row.UpdatedAt = digest, at.UTC()
	m.executions[id] = row
	return cloneExecution(row), nil
}

func (s *PgStore) PinExecutionRuntime(ctx context.Context, id, lease, digest string, at time.Time) (Execution, error) {
	if !validExecutionImageDigest(digest) || at.IsZero() {
		return Execution{}, ErrExecutionInvalid
	}
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return Execution{}, ErrExecutionInvalid
	}
	parsedLease, err := uuid.Parse(lease)
	if err != nil {
		return Execution{}, ErrExecutionInvalid
	}
	row, err := sqlc.New().ExecutionPinRuntime(ctx, s.pool, sqlc.ExecutionPinRuntimeParams{ExecutionID: pgtype.UUID{Bytes: parsedID, Valid: true}, LeaseToken: pgtype.UUID{Bytes: parsedLease, Valid: true}, ImageDigest: pgtype.Text{String: digest, Valid: true}, PinnedAt: executionTime(at)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrExecutionLeaseLost
	}
	if err != nil {
		return Execution{}, mapErr(err)
	}
	return executionFromSQL(row), nil
}
