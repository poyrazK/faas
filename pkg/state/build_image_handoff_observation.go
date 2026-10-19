package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildImageHandoffSnapshot observes all eligible recovery rows, not a recovery
// batch. A checkpoint removes a row even while preparation is still active.
type BuildImageHandoffSnapshot struct {
	Pending          int64
	OldestFinishedAt time.Time
}

type BuildImageHandoffObservationStore interface {
	BuildImageHandoffSnapshot(context.Context, string) (BuildImageHandoffSnapshot, error)
}

var (
	_ BuildImageHandoffObservationStore = (*PgStore)(nil)
	_ BuildImageHandoffObservationStore = (*MemStore)(nil)
)

func (s *PgStore) BuildImageHandoffSnapshot(ctx context.Context, node string) (BuildImageHandoffSnapshot, error) {
	r, err := sqlc.New().OperatorBuildImageHandoffSnapshot(ctx, s.pool, strings.TrimSpace(node))
	if err != nil {
		return BuildImageHandoffSnapshot{}, fmt.Errorf("state: observe build image handoffs: %w", err)
	}
	if r.MissingFinishedAt != 0 || (r.OldestFinishedAt.Valid && r.OldestFinishedAt.InfinityModifier != pgtype.Finite) {
		return BuildImageHandoffSnapshot{}, fmt.Errorf("state: build image handoff missing finite finish time")
	}
	out := BuildImageHandoffSnapshot{Pending: r.Pending}
	if r.OldestFinishedAt.Valid {
		out.OldestFinishedAt = r.OldestFinishedAt.Time
	}
	return out, nil
}

func (m *MemStore) BuildImageHandoffSnapshot(ctx context.Context, node string) (BuildImageHandoffSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BuildImageHandoffSnapshot{}, err
	}
	node = strings.TrimSpace(node)
	var out BuildImageHandoffSnapshot
	for _, b := range m.builds {
		d, exists := m.deployments[b.DeploymentID]
		if !exists || (d.Status != DeployPending && d.Status != DeployBuilding) || d.RootfsPath == "" || b.Status != BuildSucceeded {
			continue
		}
		if _, checkpointed := m.imagePreparations[d.ID]; checkpointed {
			continue
		}
		p, exists := m.buildProvenance[b.ID]
		if !exists || (node != "" && p.BuilderNodeID != "" && p.BuilderNodeID != node) {
			continue
		}
		if b.FinishedAt.IsZero() {
			return BuildImageHandoffSnapshot{}, fmt.Errorf("state: build image handoff missing finish time")
		}
		out.Pending++
		if out.Pending == 1 || b.FinishedAt.Before(out.OldestFinishedAt) {
			out.OldestFinishedAt = b.FinishedAt
		}
	}
	return out, nil
}
