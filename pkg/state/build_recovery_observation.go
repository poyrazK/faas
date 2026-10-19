package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildRecoverySnapshot observes running claims and all outstanding VM cleanup
// obligations, including live claims and retries, from one consistent read.
// Cleanup age starts when the obligation is created, never when it is retried.
type BuildRecoverySnapshot struct {
	Running                int64
	OldestStartedAt        time.Time
	PendingCleanup         int64
	OldestCleanupCreatedAt time.Time
	RetriedCleanup         int64
}

type BuildRecoveryObservationStore interface {
	BuildRecoverySnapshot(context.Context) (BuildRecoverySnapshot, error)
}

var (
	_ BuildRecoveryObservationStore = (*PgStore)(nil)
	_ BuildRecoveryObservationStore = (*MemStore)(nil)
)

func (s *PgStore) BuildRecoverySnapshot(ctx context.Context) (BuildRecoverySnapshot, error) {
	r, err := sqlc.New().OperatorBuildRecoverySnapshot(ctx, s.pool)
	if err != nil {
		return BuildRecoverySnapshot{}, fmt.Errorf("state: observe build recovery: %w", err)
	}
	if r.MissingStartedAt != 0 {
		return BuildRecoverySnapshot{}, fmt.Errorf("state: running build missing start time")
	}
	out := BuildRecoverySnapshot{Running: r.Running, PendingCleanup: r.PendingCleanup, RetriedCleanup: r.RetriedCleanup}
	for _, pair := range []struct {
		value pgtype.Timestamptz
		out   *time.Time
	}{{r.OldestStartedAt, &out.OldestStartedAt}, {r.OldestCleanupCreatedAt, &out.OldestCleanupCreatedAt}} {
		if pair.value.Valid {
			if pair.value.InfinityModifier != pgtype.Finite {
				return BuildRecoverySnapshot{}, fmt.Errorf("state: nonfinite build recovery timestamp")
			}
			*pair.out = pair.value.Time
		}
	}
	return out, nil
}

func (m *MemStore) BuildRecoverySnapshot(ctx context.Context) (BuildRecoverySnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BuildRecoverySnapshot{}, err
	}
	var out BuildRecoverySnapshot
	for _, b := range m.builds {
		if b.Status != BuildRunning {
			continue
		}
		if b.StartedAt.IsZero() {
			return BuildRecoverySnapshot{}, fmt.Errorf("state: running build missing start time")
		}
		out.Running++
		if out.Running == 1 || b.StartedAt.Before(out.OldestStartedAt) {
			out.OldestStartedAt = b.StartedAt
		}
	}
	for _, row := range m.builderVMCleanup {
		out.PendingCleanup++
		if out.PendingCleanup == 1 || row.createdAt.Before(out.OldestCleanupCreatedAt) {
			out.OldestCleanupCreatedAt = row.createdAt
		}
		if row.failedAttempt {
			out.RetriedCleanup++
		}
	}
	return out, nil
}
