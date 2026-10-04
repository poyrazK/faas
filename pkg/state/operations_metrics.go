package state

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

// Operator-only aggregate health; no customer identities or request data.
type OperationStateMetric struct {
	State           api.OperationState
	Count           int64
	OldestCreatedAt time.Time
}
type OperationMetricsSnapshot struct {
	States        []OperationStateMetric
	ActiveStreams int64
	ResultBlobs   []OperationResultBlobMetric
}
type OperationResultBlobMetric struct {
	State        string
	Count, Bytes int64
}
type OperationMetricsStore interface {
	OperationMetrics(context.Context, time.Time) (OperationMetricsSnapshot, error)
}

func (s *PgStore) OperationMetrics(ctx context.Context, now time.Time) (OperationMetricsSnapshot, error) {
	q := sqlc.New()
	at := pgtype.Timestamptz{Time: now, Valid: true}
	rows, err := q.CustomerOperationStateMetrics(ctx, s.pool, at)
	if err != nil {
		return OperationMetricsSnapshot{}, err
	}
	out := OperationMetricsSnapshot{States: make([]OperationStateMetric, 0, len(rows))}
	for _, row := range rows {
		out.States = append(out.States, OperationStateMetric{State: api.OperationState(row.State), Count: row.RetainedCount, OldestCreatedAt: row.OldestCreatedAt.Time})
	}
	out.ActiveStreams, err = q.CustomerOperationStreamMetric(ctx, s.pool, at)
	if err != nil {
		return out, err
	}
	blobs, err := q.CustomerOperationBlobMetrics(ctx, s.pool)
	for _, row := range blobs {
		out.ResultBlobs = append(out.ResultBlobs, OperationResultBlobMetric{State: row.State, Count: row.BlobCount, Bytes: row.Bytes})
	}
	return out, err
}
func (m *MemStore) OperationMetrics(_ context.Context, now time.Time) (OperationMetricsSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := OperationMetricsSnapshot{}
	counts := map[api.OperationState]OperationStateMetric{}
	data := m.operationMemoryLocked()
	for _, op := range data.operations {
		if !op.ExpiresAt.After(now) {
			continue
		}
		count := counts[op.State]
		count.State = op.State
		count.Count++
		if count.OldestCreatedAt.IsZero() || op.CreatedAt.Before(count.OldestCreatedAt) {
			count.OldestCreatedAt = op.CreatedAt
		}
		counts[op.State] = count
	}
	for _, count := range counts {
		out.States = append(out.States, count)
	}
	for _, lease := range data.streams {
		if lease.ExpiresAt.After(now) {
			out.ActiveStreams++
		}
	}
	blobs := map[string]OperationResultBlobMetric{}
	for _, blob := range data.blobs {
		row := blobs[blob.State]
		row.State = blob.State
		row.Count++
		row.Bytes += blob.SizeBytes
		blobs[blob.State] = row
	}
	for _, row := range blobs {
		out.ResultBlobs = append(out.ResultBlobs, row)
	}
	return out, nil
}
