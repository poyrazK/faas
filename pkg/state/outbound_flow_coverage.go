package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OutboundFlowCaptureSample is an immutable observation of one vmmd
// collector session. Counters are cumulative within that session, allowing
// later queries to distinguish quiet traffic from known capture loss.
type OutboundFlowCaptureSample struct {
	ID                   string
	SessionID            string
	NodeID               string
	SampledAt            time.Time
	Listening            bool
	Reason               string
	QueueDroppedTotal    int64
	DatabaseDroppedTotal int64
	UnparsedTotal        int64
	UnattributedTotal    int64
	StderrTotal          int64
}

func (s *PgStore) InsertOutboundFlowCaptureSample(ctx context.Context, sample OutboundFlowCaptureSample) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("outbound flow coverage: nil database pool")
	}
	parseID := func(name, raw string) (pgtype.UUID, error) {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("outbound flow coverage: invalid %s: %w", name, err)
		}
		return pgtype.UUID{Bytes: parsed, Valid: true}, nil
	}
	id, err := parseID("sample id", sample.ID)
	if err != nil {
		return err
	}
	sessionID, err := parseID("session id", sample.SessionID)
	if err != nil {
		return err
	}
	nodeID, err := parseID("node id", sample.NodeID)
	if err != nil {
		return err
	}
	if sample.SampledAt.IsZero() || len(sample.Reason) < 1 || len(sample.Reason) > 64 ||
		sample.QueueDroppedTotal < 0 || sample.DatabaseDroppedTotal < 0 ||
		sample.UnparsedTotal < 0 || sample.UnattributedTotal < 0 || sample.StderrTotal < 0 {
		return fmt.Errorf("outbound flow coverage: invalid sample")
	}
	_, err = sqlc.New().InsertOutboundFlowCaptureSample(ctx, s.pool, sqlc.InsertOutboundFlowCaptureSampleParams{
		ID: id, SessionID: sessionID, NodeID: nodeID,
		SampledAt: pgtype.Timestamptz{Time: sample.SampledAt.UTC(), Valid: true},
		Listening: sample.Listening, Reason: sample.Reason,
		QueueDroppedTotal:    sample.QueueDroppedTotal,
		DatabaseDroppedTotal: sample.DatabaseDroppedTotal,
		UnparsedTotal:        sample.UnparsedTotal, UnattributedTotal: sample.UnattributedTotal,
		StderrTotal: sample.StderrTotal,
	})
	if err != nil {
		return fmt.Errorf("outbound flow coverage: insert sample: %w", err)
	}
	return nil
}

func (s *PgStore) DeleteOutboundFlowCaptureSamplesBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("outbound flow coverage: nil database pool")
	}
	if limit <= 0 || limit > 10000 {
		return 0, fmt.Errorf("outbound flow coverage: invalid retention batch size %d", limit)
	}
	return sqlc.New().DeleteOutboundFlowCaptureSamplesBefore(ctx, s.pool, sqlc.DeleteOutboundFlowCaptureSamplesBeforeParams{
		Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true}, BatchSize: int32(limit),
	})
}
