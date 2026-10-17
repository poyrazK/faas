package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PostReleaseWatchCandidate is a completed release the post-release watch
// still judges (ADR-1445), with the predecessor it would roll back to.
type PostReleaseWatchCandidate struct {
	DeploymentID       string
	AppID              string
	Scope              string
	CreatedAt          time.Time
	RolloutStartedAt   time.Time
	RolloutCompletedAt time.Time
	PredecessorID      string
}

// PostReleaseWatchStore lists watched releases and records the watch's
// explained decision on a release.
type PostReleaseWatchStore interface {
	ListPostReleaseWatchCandidates(ctx context.Context, window time.Duration, limit int) ([]PostReleaseWatchCandidate, error)
	RecordPostReleaseRolloutDecision(ctx context.Context, deploymentID string, decision json.RawMessage) error
}

var (
	_ PostReleaseWatchStore = (*PgStore)(nil)
	_ PostReleaseWatchStore = (*MemStore)(nil)
)

func (s *PgStore) ListPostReleaseWatchCandidates(ctx context.Context, window time.Duration, limit int) ([]PostReleaseWatchCandidate, error) {
	rows, err := sqlc.New().ListPostReleaseWatchCandidates(ctx, s.pool, sqlc.ListPostReleaseWatchCandidatesParams{
		WindowSeconds: int32(window / time.Second), RowLimit: int32(limit)}) //nolint:gosec // bounded by api limits
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]PostReleaseWatchCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, PostReleaseWatchCandidate{DeploymentID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID),
			Scope: normalizedDeploymentScope(row.Scope), CreatedAt: row.CreatedAt.Time.UTC(),
			RolloutStartedAt: row.RolloutStartedAt.Time.UTC(), RolloutCompletedAt: row.RolloutCompletedAt.Time.UTC(),
			PredecessorID: pgUUIDString(row.PredecessorID)})
	}
	return out, nil
}

func (s *PgStore) RecordPostReleaseRolloutDecision(ctx context.Context, deploymentID string, decision json.RawMessage) error {
	if !json.Valid(decision) {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().RecordPostReleaseRolloutDecision(ctx, s.pool, sqlc.RecordPostReleaseRolloutDecisionParams{
		Decision: decision, ID: mustPgUUID(deploymentID)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *MemStore) ListPostReleaseWatchCandidates(_ context.Context, window time.Duration, limit int) ([]PostReleaseWatchCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-window)
	var out []PostReleaseWatchCandidate
	for _, d := range m.deployments {
		if d.Status != DeployLive || !d.RollbackOn5xx || d.LastAutoRollbackReason != "" || d.TrafficPercent != 100 ||
			d.RolloutState != "complete" || d.RolloutCompletedAt == nil || !d.RolloutCompletedAt.After(cutoff) {
			continue
		}
		scope := normalizedDeploymentScope(d.Scope)
		var predecessor *Deployment
		newest := true
		for _, other := range m.deployments {
			if other.AppID != d.AppID || normalizedDeploymentScope(other.Scope) != scope || other.ID == d.ID {
				continue
			}
			if other.CreatedAt.After(d.CreatedAt) && other.Status != DeployFailed && other.Status != DeployCancelled {
				newest = false
			}
			if other.Status == DeploySuperseded && other.CreatedAt.Before(d.CreatedAt) &&
				(predecessor == nil || other.CreatedAt.After(predecessor.CreatedAt)) {
				p := other
				predecessor = &p
			}
		}
		if !newest || predecessor == nil {
			continue
		}
		started := *d.RolloutCompletedAt
		if d.RolloutStartedAt != nil {
			started = *d.RolloutStartedAt
		}
		out = append(out, PostReleaseWatchCandidate{DeploymentID: d.ID, AppID: d.AppID, Scope: scope, CreatedAt: d.CreatedAt,
			RolloutStartedAt: started.UTC(), RolloutCompletedAt: d.RolloutCompletedAt.UTC(), PredecessorID: predecessor.ID})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].RolloutCompletedAt.Equal(out[j].RolloutCompletedAt) {
			return out[i].RolloutCompletedAt.Before(out[j].RolloutCompletedAt)
		}
		return out[i].DeploymentID < out[j].DeploymentID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) RecordPostReleaseRolloutDecision(_ context.Context, deploymentID string, decision json.RawMessage) error {
	if !json.Valid(decision) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return ErrNotFound
	}
	d.RolloutDecision = append(json.RawMessage(nil), decision...)
	m.deployments[deploymentID] = d
	return nil
}
