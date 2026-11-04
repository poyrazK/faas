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
// terminal builds are excluded, and so are deferred builds that are not due
// yet (ADR-20261021221906); a due one waits from its not_before. Requeue preserves the
// original enqueue time.
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
	now := time.Now()
	for id, b := range m.builds {
		if b.Status != BuildQueued || !m.buildDueLocked(id, now) {
			continue
		}
		// A deferred build has been waiting only since it became due.
		waitingSince := b.EnqueuedAt
		if notBefore, deferred := m.buildNotBefore[id]; deferred {
			waitingSince = notBefore
		}
		out.Queued++
		if out.Queued == 1 || waitingSince.Before(out.OldestEnqueuedAt) {
			out.OldestEnqueuedAt = waitingSince
		}
	}
	return out, nil
}
