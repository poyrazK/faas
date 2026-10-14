package state

import (
	"context"
	"time"
)

type RealtimeConsumerGroupAlertConfig struct {
	Enabled              bool   `json:"enabled"`
	UnassignedThreshold  string `json:"unassigned_threshold"`
	OldestPendingSeconds int    `json:"oldest_pending_seconds"`
	DeadLetterThreshold  int    `json:"dead_letter_threshold"`
	CooldownSeconds      int    `json:"cooldown_seconds"`
}
type RealtimeConsumerGroupAlert struct {
	RealtimeConsumerGroupAlertConfig
	Breached                bool       `json:"breached"`
	Unassigned              *string    `json:"unassigned"`
	OldestPendingAgeSeconds *int       `json:"oldest_pending_age_seconds"`
	DeadLetters             *int       `json:"dead_letters"`
	ObservedAt              *time.Time `json:"observed_at"`
	LastEventAt             *time.Time `json:"last_event_at"`
}
type RealtimeConsumerGroupAlertStore interface {
	ReadRealtimeConsumerGroupAlert(context.Context, string, string, string) (RealtimeConsumerGroupAlert, error)
	SetRealtimeConsumerGroupAlert(context.Context, string, string, string, RealtimeConsumerGroupAlertConfig) (RealtimeConsumerGroupAlert, error)
	EvaluateRealtimeConsumerGroupAlerts(context.Context) (int, error)
}
