package faas

import (
	"context"
	"database/sql"
	"time"
)

// Thresholds are opt-in counts; zero disables that condition. Sustain applies
// to firing and recovery, with at least two distinct fresh database observations.
type EventOutboxWatchOptions struct {
	Health           EventOutboxHealthOptions
	DueThreshold     int64
	StalledThreshold int64
	BlockedThreshold int64
	ExpiredThreshold int64
	PollInterval     time.Duration
	QueryTimeout     time.Duration
	MaxAge           time.Duration
	Sustain          time.Duration
}

// Alert is an in-process notification. This watch persists no alert state and
// sends no email/webhook. Missing proof never resolves an incident.
type EventOutboxAlert struct {
	AccountID string             `json:"account_id"`
	AppID     string             `json:"app_id"`
	Metric    string             `json:"metric"`
	Change    string             `json:"change"`
	Reason    string             `json:"reason"`
	At        time.Time          `json:"at"`
	Value     int64              `json:"value"`
	Threshold int64              `json:"threshold"`
	Health    *EventOutboxHealth `json:"health,omitempty"`
}

func eventOutboxWatchOptions(o EventOutboxWatchOptions) (EventOutboxWatchOptions, error) {
	var err error
	o.Health, err = eventOutboxHealthOptions(o.Health)
	if err != nil {
		return o, err
	}
	if o.PollInterval == 0 {
		o.PollInterval = 5 * time.Second
	}
	if o.QueryTimeout == 0 {
		o.QueryTimeout = 5 * time.Second
	}
	if o.MaxAge == 0 {
		o.MaxAge = max(3*o.PollInterval, 2*o.QueryTimeout)
	}
	if o.Sustain == 0 {
		o.Sustain = max(30*time.Second, o.PollInterval)
	}
	enabled := false
	for _, threshold := range []int64{o.DueThreshold, o.StalledThreshold, o.BlockedThreshold, o.ExpiredThreshold} {
		if threshold < 0 || threshold > int64(o.Health.ScanLimit) {
			return o, ErrInvalidEventConsumerRequest
		}
		enabled = enabled || threshold > 0
	}
	if !enabled || o.PollInterval < 100*time.Millisecond || o.PollInterval > time.Minute || o.QueryTimeout < 10*time.Millisecond || o.QueryTimeout > 30*time.Second || o.MaxAge < 2*o.PollInterval || o.MaxAge < o.QueryTimeout || o.MaxAge > 5*time.Minute || o.Sustain < o.PollInterval || o.Sustain > 24*time.Hour {
		return o, ErrInvalidEventConsumerRequest
	}
	return o, nil
}

// WatchEventOutboxHealth polls metadata sequentially until cancellation.
// The caller owns process lifetime and the synchronous local notification callback.
// Reads that fail, overflow, repeat, regress or become stale reset confirmation.
func WatchEventOutboxHealth(ctx context.Context, db *sql.DB, accountID, appID string, options EventOutboxWatchOptions, notify func(EventOutboxAlert)) error {
	if ctx == nil || db == nil || notify == nil {
		return ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return err
	}
	options, err = eventOutboxWatchOptions(options)
	if err != nil {
		return err
	}
	conditions := []eventIntentWatchCondition{}
	if options.DueThreshold > 0 {
		conditions = append(conditions, eventIntentWatchCondition{metric: "due_backlog", threshold: options.DueThreshold, state: "unknown"})
	}
	if options.StalledThreshold > 0 {
		conditions = append(conditions, eventIntentWatchCondition{metric: "stalled_recovery", threshold: options.StalledThreshold, state: "unknown"})
	}
	if options.BlockedThreshold > 0 {
		conditions = append(conditions, eventIntentWatchCondition{metric: "blocked_publications", threshold: options.BlockedThreshold, state: "unknown"})
	}
	if options.ExpiredThreshold > 0 {
		conditions = append(conditions, eventIntentWatchCondition{metric: "expired_publications", threshold: options.ExpiredThreshold, state: "unknown"})
	}
	var previousObserved, previousPoll time.Time
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			callCtx, cancel := context.WithTimeout(ctx, options.QueryTimeout)
			health, readErr := GetEventOutboxHealth(callCtx, db, account, app, options.Health)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			now := time.Now()
			reason := eventRecoveryHealthEvidence(eventRecoveryHealthObservation{ObservedAt: health.ObservedAt, Complete: health.Complete, Truncated: health.Truncated, InvalidRows: health.InvalidRows}, readErr, previousObserved, previousPoll, now, options.MaxAge)
			previousPoll = now
			if readErr == nil && !health.ObservedAt.IsZero() && !health.ObservedAt.After(now) && now.Sub(health.ObservedAt) <= options.MaxAge && health.ObservedAt.After(previousObserved) {
				previousObserved = health.ObservedAt
			}
			for i := range conditions {
				value := health.Due
				switch conditions[i].metric {
				case "stalled_recovery":
					value = health.Stalled
				case "blocked_publications":
					value = health.Blocked
				case "expired_publications":
					value = health.Expired
				}
				var snapshot *EventOutboxHealth
				if readErr == nil {
					copy := health
					snapshot = &copy
				}
				emit := func(change, why string) {
					notify(EventOutboxAlert{AccountID: account, AppID: app, Metric: conditions[i].metric, Change: change, Reason: why, At: now, Value: value, Threshold: conditions[i].threshold, Health: snapshot})
				}
				conditionReason := reason
				if conditionReason == "" && health.PublishingPaused && (conditions[i].metric == "due_backlog" || conditions[i].metric == "stalled_recovery") {
					conditionReason = "publishing_paused"
				}
				if conditionReason == "" && health.PublishingRateLimited && (conditions[i].metric == "due_backlog" || conditions[i].metric == "stalled_recovery") {
					conditionReason = "publishing_rate_limited"
				}
				conditions[i].update(value, conditionReason, now, options.Sustain, emit)
			}
			timer.Reset(options.PollInterval)
		}
	}
}
