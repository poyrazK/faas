package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	deadlinesqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
	"time"
)

func validateRealtimeGroupDeadlineKey(key string) error {
	if key != "" && (len(key) > api.RealtimeConsumerGroupOrderingFieldMaxBytes || api.ValidateRealtimeMetadata(map[string]string{key: ""}) != nil) {
		return ErrRealtimeConsumerGroupInvalid
	}
	return nil
}
func freezeRealtimeGroupDeadline(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64, metadata map[string]string) error {
	var deadline pgtype.Timestamptz
	invalid := false
	if g.deadlineField != "" && metadata[g.deadlineField] != "" {
		parsed, err := time.Parse(time.RFC3339Nano, metadata[g.deadlineField])
		invalid = err != nil || parsed.Year() < 1 || parsed.Year() > 9999
		if !invalid {
			deadline = pgtype.Timestamptz{Time: parsed.UTC().Truncate(time.Microsecond), Valid: true}
		}
	}
	return deadlinesqlc.New().FreezeDeadline(ctx, tx, deadlinesqlc.FreezeDeadlineParams{GroupID: g.id, Sequence: seq, DeliveryDeadline: deadline, DeadlineInvalid: invalid})
}
func expireRealtimeGroupDeadlines(ctx context.Context, tx pgx.Tx, g realtimeGroupRow) error {
	return deadlinesqlc.New().ExpireDeadlines(ctx, tx, g.id)
}
func realtimeGroupReplayDeadline(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64) (bool, error) {
	return deadlinesqlc.New().ReplayDeadlineRejected(ctx, tx, deadlinesqlc.ReplayDeadlineRejectedParams{GroupID: g.id, Sequence: seq})
}
