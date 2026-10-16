package mcpmanagement

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Read-time observations for one saved revision, never a retention lease or
// evidence that downstream processing succeeded. Nil counts mean no baseline.
type activityConsumerLag struct {
	ObservedAt                  time.Time `json:"observed_at"`
	RetentionSeconds            int       `json:"retention_seconds"`
	RetainedPendingEvents       *int64    `json:"retained_pending_events,omitempty"`
	ExpiredUnacknowledgedEvents *int64    `json:"expired_unacknowledged_events,omitempty"`
	SecondsSinceAck             *float64  `json:"seconds_since_ack,omitempty"`
	AckTimeInFuture             bool      `json:"ack_time_in_future,omitempty"`
}

func consumerLag(b activityConsumerBinding, record activityConsumerRecord, point api.EnvironmentCacheActivityRecovery, at time.Time) (*activityConsumerLag, error) {
	if err := validateScheduleActivityCheckpoint(point, b.Account, b.App, b.Project, b.Environment, b.Scope); err != nil {
		return nil, err
	}
	out := &activityConsumerLag{ObservedAt: at, RetentionSeconds: point.RetentionSeconds}
	if record.Revision == 0 {
		return out, nil
	}
	saved, err := api.ParseEnvironmentCacheActivityCursor(record.Cursor, b.Account, b.App, b.Project, b.Environment)
	floor, _ := api.ParseEnvironmentCacheActivityCursor(point.OldestCursor, b.Account, b.App, b.Project, b.Environment)
	head, _ := api.ParseEnvironmentCacheActivityCursor(point.RestartCursor, b.Account, b.App, b.Project, b.Environment)
	if err != nil || record.Cursor == "" || saved.Sequence > head.Sequence {
		return nil, invalid("invalid_response", "consumer progress exceeds observed feed bounds")
	}
	pending := head.Sequence - max(saved.Sequence, floor.Sequence)
	expired := max(int64(0), floor.Sequence-saved.Sequence)
	out.RetainedPendingEvents, out.ExpiredUnacknowledgedEvents = &pending, &expired
	age := at.Sub(record.UpdatedAt).Seconds()
	if age < 0 {
		out.AckTimeInFuture = true
	} else {
		out.SecondsSinceAck = &age
	}
	return out, nil
}
