package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildQueueSnapshot describes queued rows from one database snapshot (or one
// MemStore lock). An empty queue has a zero OldestEnqueuedAt. Running and
// terminal builds are excluded; requeue preserves the original enqueue time.
type BuildQueueSnapshot struct {
	Queued           int64
	OldestEnqueuedAt time.Time
}

// BuildQueueObservationStore is the bounded observer's optional read contract.
// Keeping it separate from Store avoids a fallback to two inconsistent reads
// in adapters that only implement the legacy count endpoint.
type BuildQueueObservationStore interface {
	QueuedBuildsSnapshot(context.Context) (BuildQueueSnapshot, error)
}

var (
	_ BuildQueueObservationStore = (*PgStore)(nil)
	_ BuildQueueObservationStore = (*MemStore)(nil)
)

func (s *PgStore) QueuedBuildsSnapshot(ctx context.Context) (BuildQueueSnapshot, error) {
	row, err := sqlc.New().OperatorQueuedBuildsSnapshot(ctx, s.pool)
	if err != nil {
		return BuildQueueSnapshot{}, fmt.Errorf("state: observe queued builds: %w", err)
	}
	out := BuildQueueSnapshot{Queued: row.Queued}
	if row.OldestEnqueuedAt.Valid {
		if row.OldestEnqueuedAt.InfinityModifier != pgtype.Finite {
			return BuildQueueSnapshot{}, fmt.Errorf("state: nonfinite queued-build timestamp")
		}
		out.OldestEnqueuedAt = row.OldestEnqueuedAt.Time
	}
	return out, nil
}

func (m *MemStore) QueuedBuildsSnapshot(ctx context.Context) (BuildQueueSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BuildQueueSnapshot{}, err
	}
	var out BuildQueueSnapshot
	for _, b := range m.builds {
		if b.Status != BuildQueued {
			continue
		}
		out.Queued++
		if out.Queued == 1 || b.EnqueuedAt.Before(out.OldestEnqueuedAt) {
			out.OldestEnqueuedAt = b.EnqueuedAt
		}
	}
	return out, nil
}
