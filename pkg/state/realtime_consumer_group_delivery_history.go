package state

import (
	"context"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	historysqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeConsumerGroupDeliveryHistoryEvent struct {
	ID           string    `json:"id"`
	Sequence     string    `json:"sequence"`
	Event        string    `json:"event"`
	Attempt      int       `json:"attempt"`
	FencingToken *string   `json:"fencing_token"`
	FailureCode  string    `json:"failure_code"`
	RecordedAt   time.Time `json:"recorded_at"`
}
type RealtimeConsumerGroupDeliveryHistory struct {
	Events            []RealtimeConsumerGroupDeliveryHistoryEvent `json:"events"`
	HasMore           bool                                        `json:"has_more"`
	NextAfterID       string                                      `json:"next_after_id"`
	GroupPrunedEvents string                                      `json:"group_pruned_events"`
	RetentionSeconds  int                                         `json:"retention_seconds"`
}
type RealtimeConsumerGroupDeliveryHistoryStore interface {
	ReadRealtimeConsumerGroupDeliveryHistory(context.Context, string, string, string, string, string, int) (RealtimeConsumerGroupDeliveryHistory, error)
}

func (s *PgStore) ReadRealtimeConsumerGroupDeliveryHistory(ctx context.Context, ep, ch, name, sequence, afterID string, limit int) (RealtimeConsumerGroupDeliveryHistory, error) {
	result := RealtimeConsumerGroupDeliveryHistory{Events: []RealtimeConsumerGroupDeliveryHistoryEvent{}, NextAfterID: afterID, GroupPrunedEvents: "0", RetentionSeconds: api.RealtimeConsumerGroupDeliveryHistoryRetentionSeconds}
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return result, ErrRealtimeWorkerCredentialDenied
	}
	seq, err := realtimeGroupSequence(sequence)
	if err != nil || seq == 0 {
		return result, ErrRealtimeConsumerGroupInvalid
	}
	after, err := realtimeGroupSequence(afterID)
	if err != nil || limit < 1 || limit > api.RealtimeConsumerGroupDeliveryHistoryPageMax {
		return result, ErrRealtimeConsumerGroupInvalid
	}
	result.NextAfterID = strconv.FormatInt(after, 10)
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = historysqlc.New().PruneExpiredDeliveryHistory(ctx, tx, historysqlc.PruneExpiredDeliveryHistoryParams{GroupID: g.id, RetentionSeconds: api.RealtimeConsumerGroupDeliveryHistoryRetentionSeconds})
	if err != nil {
		return result, err
	}
	result.GroupPrunedEvents, err = historysqlc.New().ReadPrunedDeliveryEvents(ctx, tx, g.id)
	if err != nil {
		return result, err
	}
	rows, err := historysqlc.New().ReadDeliveryAttemptHistory(ctx, tx, historysqlc.ReadDeliveryAttemptHistoryParams{GroupID: g.id, Sequence: seq, AfterID: after, RowLimit: int32(limit + 1), RetentionSeconds: api.RealtimeConsumerGroupDeliveryHistoryRetentionSeconds})
	if err != nil {
		return result, err
	}
	result.HasMore = len(rows) > limit
	if result.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		item := RealtimeConsumerGroupDeliveryHistoryEvent{ID: row.ID, Sequence: row.Sequence, Event: row.Event, Attempt: int(row.Attempt), FailureCode: row.FailureCode, RecordedAt: row.RecordedAt.Time.UTC()}
		if row.FencingToken != "" {
			fence := row.FencingToken
			item.FencingToken = &fence
		}
		result.Events = append(result.Events, item)
		result.NextAfterID = row.ID
	}
	return result, tx.Commit(ctx)
}
