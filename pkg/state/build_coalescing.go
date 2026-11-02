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

// BuildCoalescingWindow bounds how long a running build counts as building
// its cache key. A build claimed longer ago is stale (its builder may be gone),
// so nothing waits for it. It is twice the builder VM's 15-minute limit
// (ADR-791).
const BuildCoalescingWindow = 30 * time.Minute

// BuildCoalescingStore lets builderd defer a build that missed the cache while
// an identical build (same cache key) is already building, instead of building
// the same image again (ADR-20261102100000). Claims skip the deferred build
// while that build is queued or running, then prefer the node that built it,
// whose cache now holds the result.
type BuildCoalescingStore interface {
	// RunningIdenticalBuild returns the ID of the earliest-claimed other build
	// that is building keySHA256 now and was claimed before claim, or
	// ErrNotFound.
	RunningIdenticalBuild(ctx context.Context, claim Build, keySHA256 string) (string, error)
	// DeferBuildBehind requeues claim to wait for producerID. It returns
	// ErrNotFound when claim is no longer the current running claim.
	DeferBuildBehind(ctx context.Context, claim Build, producerID string) error
}

var _ BuildCoalescingStore = (*PgStore)(nil)
var _ BuildCoalescingStore = (*MemStore)(nil)

// RunningIdenticalBuild implements BuildCoalescingStore.
func (s *PgStore) RunningIdenticalBuild(ctx context.Context, claim Build, keySHA256 string) (string, error) {
	id, err := sqlc.New().RunningIdenticalBuild(ctx, s.pool, sqlc.RunningIdenticalBuildParams{
		CacheKeySha256: keySHA256,
		ClaimID:        mustPgUUID(claim.ID),
		ClaimStartedAt: pgtype.Timestamptz{Time: claim.StartedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: find running identical build for %s: %w", claim.ID, err)
	}
	return uuidString(id), nil
}

// DeferBuildBehind implements BuildCoalescingStore.
func (s *PgStore) DeferBuildBehind(ctx context.Context, claim Build, producerID string) error {
	n, err := sqlc.New().DeferBuildBehind(ctx, s.pool, sqlc.DeferBuildBehindParams{
		ProducerID:     mustPgUUID(producerID),
		ClaimID:        mustPgUUID(claim.ID),
		DeploymentID:   mustPgUUID(claim.DeploymentID),
		ClaimStartedAt: pgtype.Timestamptz{Time: claim.StartedAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("state: defer build %s behind %s: %w", claim.ID, producerID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RunningIdenticalBuild implements BuildCoalescingStore.
func (m *MemStore) RunningIdenticalBuild(_ context.Context, claim Build, keySHA256 string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	pick := ""
	var pickAt time.Time
	for id, b := range m.builds {
		if id == claim.ID || b.Status != BuildRunning || b.CacheStatus != "miss" || b.CacheKeySHA256 != keySHA256 ||
			!b.StartedAt.After(now.Add(-BuildCoalescingWindow)) {
			continue
		}
		claimedEarlier := b.StartedAt.Before(claim.StartedAt) || (b.StartedAt.Equal(claim.StartedAt) && id < claim.ID)
		if !claimedEarlier {
			continue
		}
		if pick == "" || b.StartedAt.Before(pickAt) || (b.StartedAt.Equal(pickAt) && id < pick) {
			pick, pickAt = id, b.StartedAt
		}
	}
	if pick == "" {
		return "", ErrNotFound
	}
	return pick, nil
}

// DeferBuildBehind implements BuildCoalescingStore.
func (m *MemStore) DeferBuildBehind(_ context.Context, claim Build, producerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[claim.ID]
	if !ok || producerID == claim.ID || b.Status != BuildRunning || b.DeploymentID != claim.DeploymentID || !b.StartedAt.Equal(claim.StartedAt) {
		return ErrNotFound
	}
	b.Status = BuildQueued
	b.StartedAt = time.Time{}
	m.builds[claim.ID] = b
	m.buildCoalescedWith[claim.ID] = producerID
	return nil
}

// buildWaitsForProducerLocked mirrors the claim queries' coalescing predicate:
// a deferred build waits while the build it waits for is queued, or running
// and not stale.
func (m *MemStore) buildWaitsForProducerLocked(build Build, now time.Time) bool {
	producer, ok := m.builds[m.buildCoalescedWith[build.ID]]
	if !ok {
		return false
	}
	return producer.Status == BuildQueued ||
		producer.Status == BuildRunning && producer.StartedAt.After(now.Add(-BuildCoalescingWindow))
}

// coalescedBuildEligibleOnNodeLocked mirrors the node-affinity claims for a
// deferred build: the node that built its producer may claim it at once, any
// node once affinityGrace has passed since the producer finished.
func (m *MemStore) coalescedBuildEligibleOnNodeLocked(build Build, nodeID string, affinityGrace time.Duration, now time.Time) (eligible, coalesced bool) {
	producerID, ok := m.buildCoalescedWith[build.ID]
	if !ok {
		return false, false
	}
	producer, ok := m.builds[producerID]
	if !ok || !producer.FinishedAt.After(now.Add(-affinityGrace)) {
		return true, true
	}
	node := m.buildProvenance[producerID].BuilderNodeID
	return node == "" || node == nodeID, true
}
