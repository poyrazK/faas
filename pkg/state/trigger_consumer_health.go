package state

import (
	"context"
	"time"
)

// TriggerConsumerHealth is the durable last-known health snapshot for one
// trigger consumer. It is intentionally separate from the trigger row so the
// scheduler can update liveness without mutating customer configuration.
type TriggerConsumerHealth struct {
	LastPollAt    *time.Time
	LastSuccessAt *time.Time
	LastErrorAt   *time.Time
	LastError     string
	LagMessages   *int64
	LagAgeSeconds *float64
}

// TriggerConsumerHealthObservation is the scheduler-owned write shape. A
// successful observation may carry a broker-native lag snapshot; nil lag
// fields mean that the source does not expose native lag.
type TriggerConsumerHealthObservation struct {
	LastPollAt    time.Time
	Success       bool
	Error         string
	LagMessages   *int64
	LagAgeSeconds *float64
}

// TriggerConsumerHealthStore is optional so older narrow test stores and
// alternate state adapters can continue to run without a health table.
type TriggerConsumerHealthStore interface {
	RecordTriggerConsumerHealth(context.Context, string, TriggerConsumerHealthObservation) error
	TriggerConsumerHealth(context.Context, string) (TriggerConsumerHealth, error)
}
