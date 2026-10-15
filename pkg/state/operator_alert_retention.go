package state

import (
	"context"
	"time"
)

type OperatorAlertRetention struct {
	LastAttemptAt, LastSuccessAt time.Time
	LastPrunedEpisodes           int64
	ExpiredEpisodes              int64
	OldestExpiredAt              time.Time
}

type OperatorAlertRetentionStore interface {
	PruneOperatorAlertHistory(context.Context, time.Time, int) (int, error)
	RecordOperatorAlertRetentionFailure(context.Context, time.Time) error
	OperatorAlertRetentionStatus(context.Context, time.Time) (OperatorAlertRetention, error)
}
