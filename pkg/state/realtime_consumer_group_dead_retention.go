package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

type RealtimeConsumerGroupRetentionPolicy struct {
	RetentionSeconds   int    `json:"retention_seconds"`
	WarningSeconds     int    `json:"warning_seconds"`
	ExpiredDeadLetters string `json:"expired_dead_letters"`
}
type RealtimeConsumerGroupRetentionStore interface {
	ReadRealtimeConsumerGroupRetention(context.Context, string, string, string) (RealtimeConsumerGroupRetentionPolicy, error)
	SetRealtimeConsumerGroupRetention(context.Context, string, string, string, int) (RealtimeConsumerGroupRetentionPolicy, error)
	ExpireRealtimeConsumerGroupDeadLetters(context.Context) (int, error)
}

func validateRealtimeGroupRetention(seconds int, field, policy string) error {
	if seconds == 0 {
		return nil
	}
	if seconds < api.RealtimeConsumerGroupDeadRetentionMinSeconds || seconds > api.RealtimeConsumerGroupDeadRetentionMaxSeconds || (field != "" && policy != "skip") {
		return ErrRealtimeConsumerGroupInvalid
	}
	return nil
}
