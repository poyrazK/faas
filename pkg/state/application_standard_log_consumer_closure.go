package state

import (
	"context"
	"time"
)

// Closure is recorded only after the gateway's sender and source workers join.
// It invalidates reports from this startup; it does not remove a fleet obligation.
type ApplicationStandardLogConsumerClosure struct {
	ApplicationStandardLogConsumerSession
	StoppedAt time.Time `json:"stopped_at"`
}

type ApplicationStandardLogConsumerClosureStore interface {
	CloseApplicationStandardLogConsumer(context.Context, ApplicationStandardLogConsumerSession) (ApplicationStandardLogConsumerClosure, error)
}
