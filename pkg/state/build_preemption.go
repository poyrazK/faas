package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildPreemptionStore records elastic builder slots' give-backs
// (ADR-20261118154632): when tenants need the memory a borrowed builder slot
// is using, builderd returns the build to the queue and counts it, and a build
// given back api.BuildMaxPreemptions times runs only on a reserved slot.
type BuildPreemptionStore interface {
	// BuildPreemptions returns how many times borrowed slots gave buildID
	// back, or ErrNotFound.
	BuildPreemptions(ctx context.Context, buildID string) (int, error)
	// RequeuePreemptedBuild returns claim to the queue, keeping its
	// enqueued_at, and counts the give-back. It returns ErrNotFound when
	// claim is no longer the current running claim.
	RequeuePreemptedBuild(ctx context.Context, claim Build) error
}

var _ BuildPreemptionStore = (*PgStore)(nil)
var _ BuildPreemptionStore = (*MemStore)(nil)

// BuildPreemptions implements BuildPreemptionStore.
func (s *PgStore) BuildPreemptions(ctx context.Context, buildID string) (int, error) {
	n, err := sqlc.New().BuildPreemptions(ctx, s.pool, mustPgUUID(buildID))
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("state: read preemptions of build %s: %w", buildID, err)
	}
	return int(n), nil
}

// RequeuePreemptedBuild implements BuildPreemptionStore.
func (s *PgStore) RequeuePreemptedBuild(ctx context.Context, claim Build) error {
	n, err := sqlc.New().RequeuePreemptedBuild(ctx, s.pool, sqlc.RequeuePreemptedBuildParams{
		ClaimID:        mustPgUUID(claim.ID),
		DeploymentID:   mustPgUUID(claim.DeploymentID),
		ClaimStartedAt: pgtype.Timestamptz{Time: claim.StartedAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("state: requeue preempted build %s: %w", claim.ID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// BuildPreemptions implements BuildPreemptionStore.
func (m *MemStore) BuildPreemptions(_ context.Context, buildID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.builds[buildID]; !ok {
		return 0, ErrNotFound
	}
	return m.buildPreemptions[buildID], nil
}

// RequeuePreemptedBuild implements BuildPreemptionStore.
func (m *MemStore) RequeuePreemptedBuild(_ context.Context, claim Build) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[claim.ID]
	if !ok || b.Status != BuildRunning || b.DeploymentID != claim.DeploymentID || !b.StartedAt.Equal(claim.StartedAt) {
		return ErrNotFound
	}
	b.Status = BuildQueued
	b.StartedAt = time.Time{}
	m.builds[claim.ID] = b
	m.buildPreemptions[claim.ID]++
	return nil
}
