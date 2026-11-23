package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildQueueReportReader is an optional read capability for the operator
// build queue report (ADR-20261123163340); Store stays unchanged.
type BuildQueueReportReader interface {
	// OperatorActiveBuilds returns queued and running builds, the longest
	// waiting first, at most limit.
	OperatorActiveBuilds(ctx context.Context, limit int) ([]OperatorBuildRow, error)
	// OperatorFinishedBuildsSince returns builds that reached a terminal
	// status at or after since, newest first, at most limit.
	OperatorFinishedBuildsSince(ctx context.Context, since time.Time, limit int) ([]OperatorBuildRow, error)
}

// OperatorBuildRow is one build with its app. NodeName is the compute node
// that claimed it (empty while queued). Zero times are unset.
type OperatorBuildRow struct {
	ID, DeploymentID, AppID, AppSlug, AccountID  string
	Kind, Status, FailureClass, CacheStatus      string
	NodeName, CoalescedWith                      string
	SourceBytes                                  int64
	Preemptions                                  int
	EnqueuedAt, NotBefore, StartedAt, FinishedAt time.Time
}

var (
	_ BuildQueueReportReader = (*PgStore)(nil)
	_ BuildQueueReportReader = (*MemStore)(nil)
)

func (s *PgStore) OperatorActiveBuilds(ctx context.Context, limit int) ([]OperatorBuildRow, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().OperatorActiveBuilds(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("state: operator active builds: %w", err)
	}
	out := make([]OperatorBuildRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, operatorBuildRow(sqlc.OperatorFinishedBuildsSinceRow(r)))
	}
	return out, nil
}

func (s *PgStore) OperatorFinishedBuildsSince(ctx context.Context, since time.Time, limit int) ([]OperatorBuildRow, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().OperatorFinishedBuildsSince(ctx, s.pool, sqlc.OperatorFinishedBuildsSinceParams{
		Since: pgtype.Timestamptz{Time: since, Valid: true}, RowLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("state: operator finished builds: %w", err)
	}
	out := make([]OperatorBuildRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, operatorBuildRow(r))
	}
	return out, nil
}

func operatorBuildRow(r sqlc.OperatorFinishedBuildsSinceRow) OperatorBuildRow {
	return OperatorBuildRow{ID: r.ID, DeploymentID: r.DeploymentID, AppID: r.AppID, AppSlug: r.AppSlug, AccountID: r.AccountID,
		Kind: r.Kind, Status: r.Status, FailureClass: r.FailureClass, CacheStatus: r.CacheStatus, NodeName: r.BuilderNodeName,
		CoalescedWith: r.CoalescedWith, SourceBytes: r.SourceBytes, Preemptions: int(r.Preemptions),
		EnqueuedAt: r.EnqueuedAt.Time, NotBefore: r.NotBefore.Time, StartedAt: r.StartedAt.Time, FinishedAt: r.FinishedAt.Time}
}

// memOperatorBuildRowLocked joins a build to its app. Callers hold m.mu.
func (m *MemStore) memOperatorBuildRowLocked(b Build) OperatorBuildRow {
	row := OperatorBuildRow{ID: b.ID, DeploymentID: b.DeploymentID, Kind: string(b.Kind), Status: string(b.Status),
		FailureClass: string(b.FailureClass), CacheStatus: b.CacheStatus, CoalescedWith: m.buildCoalescedWith[b.ID],
		SourceBytes: b.SourceBytes, Preemptions: m.buildPreemptions[b.ID], EnqueuedAt: b.EnqueuedAt,
		NotBefore: m.buildNotBefore[b.ID], StartedAt: b.StartedAt, FinishedAt: b.FinishedAt}
	if d, ok := m.deployments[b.DeploymentID]; ok {
		row.AppID = d.AppID
		if a, ok := m.apps[d.AppID]; ok {
			row.AppSlug, row.AccountID = a.Slug, a.AccountID
		}
	}
	return row
}

func (m *MemStore) OperatorActiveBuilds(_ context.Context, limit int) ([]OperatorBuildRow, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OperatorBuildRow
	for _, b := range m.builds {
		if b.Status == BuildQueued || b.Status == BuildRunning {
			out = append(out, m.memOperatorBuildRowLocked(b))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].EnqueuedAt.Equal(out[j].EnqueuedAt) {
			return out[i].EnqueuedAt.Before(out[j].EnqueuedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) OperatorFinishedBuildsSince(_ context.Context, since time.Time, limit int) ([]OperatorBuildRow, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OperatorBuildRow
	for _, b := range m.builds {
		terminal := b.Status == BuildSucceeded || b.Status == BuildFailed || b.Status == BuildCancelled
		if terminal && !b.FinishedAt.IsZero() && !b.FinishedAt.Before(since) {
			out = append(out, m.memOperatorBuildRowLocked(b))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].FinishedAt.Equal(out[j].FinishedAt) {
			return out[i].FinishedAt.After(out[j].FinishedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
