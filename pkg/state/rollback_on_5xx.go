package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RollbackOn5xxCandidate is one completed live release still eligible for
// first-wake 5xx auto-rollback (ADR-625). WindowEndsAt is nil until apid has
// observed traffic for the release and stamped FirstWakeAt.
type RollbackOn5xxCandidate struct {
	DeploymentID string
	AppID        string
	Scope        string
	CreatedAt    time.Time
	FirstWakeAt  *time.Time
	WindowEndsAt *time.Time
}

// RollbackOn5xxStore lists releases the apid rollback-on-5xx worker evaluates.
// A release qualifies while it is live at 100% with a complete rollout, opted
// into rollback_on_5xx, has not auto-rolled back, and its first-wake window
// is unopened or closed less than grace ago.
type RollbackOn5xxStore interface {
	ListRollbackOn5xxCandidates(ctx context.Context, grace time.Duration, limit int) ([]RollbackOn5xxCandidate, error)
}

// ListRollbackOn5xxCandidates implements RollbackOn5xxStore.
func (s *PgStore) ListRollbackOn5xxCandidates(ctx context.Context, grace time.Duration, limit int) ([]RollbackOn5xxCandidate, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("state: ListRollbackOn5xxCandidates: limit must be positive: %w", ErrInvalidArgument)
	}
	rows, err := sqlc.New().ListRollbackOn5xxCandidates(ctx, s.pool, sqlc.ListRollbackOn5xxCandidatesParams{
		GraceSeconds: int32(max(grace, 0) / time.Second), //nolint:gosec // bounded by the caller's grace constant
		RowLimit:     int32(min(limit, 1000)),            //nolint:gosec // clamped above
	})
	if err != nil {
		return nil, fmt.Errorf("state: ListRollbackOn5xxCandidates: %w", mapErr(err))
	}
	out := make([]RollbackOn5xxCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, RollbackOn5xxCandidate{
			DeploymentID: operationUUIDString(row.ID),
			AppID:        operationUUIDString(row.AppID),
			Scope:        row.Scope,
			CreatedAt:    row.CreatedAt.Time.UTC(),
			FirstWakeAt:  invocationTimePointer(row.FirstWakeAt),
			WindowEndsAt: invocationTimePointer(row.First5xxWindowEndsAt),
		})
	}
	return out, nil
}

// ListRollbackOn5xxCandidates mirrors PgStore.ListRollbackOn5xxCandidates.
func (m *MemStore) ListRollbackOn5xxCandidates(_ context.Context, grace time.Duration, limit int) ([]RollbackOn5xxCandidate, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("state: ListRollbackOn5xxCandidates: limit must be positive: %w", ErrInvalidArgument)
	}
	cutoff := time.Now().Add(-max(grace, 0))
	m.mu.Lock()
	out := make([]RollbackOn5xxCandidate, 0)
	for _, d := range m.deployments {
		if d.Status != DeployLive || !d.RollbackOn5xx || d.LastAutoRollbackReason != "" ||
			d.TrafficPercent != 100 || d.RolloutState != "complete" ||
			d.First5xxWindowEndsAt != nil && !d.First5xxWindowEndsAt.After(cutoff) {
			continue
		}
		out = append(out, RollbackOn5xxCandidate{
			DeploymentID: d.ID, AppID: d.AppID, Scope: d.Scope, CreatedAt: d.CreatedAt.UTC(),
			FirstWakeAt: d.FirstWakeAt, WindowEndsAt: d.First5xxWindowEndsAt,
		})
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].DeploymentID < out[j].DeploymentID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
