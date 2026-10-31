package preview

import (
	"context"
	"database/sql"
	"time"
)

// EventAcknowledgmentIntentHealthMetricsWatchOptions bounds sequential reads.
type EventAcknowledgmentIntentHealthMetricsWatchOptions struct {
	Health       EventAcknowledgmentIntentHealthOptions
	PollInterval time.Duration // Whole milliseconds 100..60000; zero defaults to 5 seconds.
	QueryTimeout time.Duration // Whole milliseconds 10..30000; zero defaults to 5 seconds.
}

func eventIntentHealthMetricsWatchOptions(o EventAcknowledgmentIntentHealthMetricsWatchOptions) (EventAcknowledgmentIntentHealthMetricsWatchOptions, error) {
	var err error
	o.Health, err = eventIntentHealthOptions(o.Health)
	if err != nil {
		return o, err
	}
	if o.PollInterval == 0 {
		o.PollInterval = 5 * time.Second
	}
	if o.QueryTimeout == 0 {
		o.QueryTimeout = 5 * time.Second
	}
	if o.PollInterval < 100*time.Millisecond || o.PollInterval > time.Minute || o.PollInterval%time.Millisecond != 0 || o.QueryTimeout < 10*time.Millisecond || o.QueryTimeout > 30*time.Second || o.QueryTimeout%time.Millisecond != 0 {
		return o, ErrInvalidEventConsumerRequest
	}
	return o, nil
}

// WatchEventAcknowledgmentIntentHealthMetrics explicitly polls one matching
// scope/cache until context cancellation. Only one poller may own a cache.
// Reads are synchronous and sequential: no active database call is abandoned or
// raced. Cooperative query deadlines include acquisition and Scan; the host must
// also bound driver/cleanup calls. Failed, late or malformed reads clear evidence.
// Cancellation joins the active read, clears cached counts, and returns ctx.Err().
// Keep the pool available until this call returns. No recovery or callback runs.
func WatchEventAcknowledgmentIntentHealthMetrics(ctx context.Context, db *sql.DB, accountID, appID string, metrics *EventAcknowledgmentIntentHealthMetrics, options EventAcknowledgmentIntentHealthMetricsWatchOptions) error {
	if ctx == nil || db == nil {
		return ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return err
	}
	options, err = eventIntentHealthMetricsWatchOptions(options)
	if err != nil {
		return err
	}
	if err = metrics.beginPolling(account, app); err != nil {
		return err
	}
	defer metrics.endPolling()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			started := time.Now()
			readCtx, cancel := context.WithTimeout(ctx, options.QueryTimeout)
			health, readErr := GetEventAcknowledgmentIntentHealth(readCtx, db, account, app, options.Health)
			late := readCtx.Err() != nil || time.Since(started) >= options.QueryTimeout
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if readErr == nil && !late {
				readErr = metrics.Record(health)
			}
			if readErr != nil || late {
				metrics.RecordReadFailure()
			}
			timer.Reset(options.PollInterval)
		}
	}
}
