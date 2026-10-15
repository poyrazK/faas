package faas

import (
	"context"
	"database/sql"
	"time"
)

// Both thresholds are opt-in counts; zero disables that condition. Sustain applies
// to firing and recovery, with at least two distinct fresh database observations.
type EventAcknowledgmentIntentWatchOptions struct {
	Health           EventAcknowledgmentIntentHealthOptions
	DueThreshold     int64
	StalledThreshold int64
	PollInterval     time.Duration
	QueryTimeout     time.Duration
	MaxAge           time.Duration
	Sustain          time.Duration
}

// Alert is an in-process notification. This watch persists no alert state and
// sends no email/webhook. Missing proof never resolves an incident.
type EventAcknowledgmentIntentAlert struct {
	AccountID string                           `json:"account_id"`
	AppID     string                           `json:"app_id"`
	Metric    string                           `json:"metric"`
	Change    string                           `json:"change"`
	Reason    string                           `json:"reason"`
	At        time.Time                        `json:"at"`
	Value     int64                            `json:"value"`
	Threshold int64                            `json:"threshold"`
	Health    *EventAcknowledgmentIntentHealth `json:"health,omitempty"`
}
type eventIntentWatchCondition struct {
	metric    string
	threshold int64
	state     string
	reason    string
	first     time.Time
	samples   int
	clearing  bool
}

func eventIntentWatchOptions(o EventAcknowledgmentIntentWatchOptions) (EventAcknowledgmentIntentWatchOptions, error) {
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
	if o.MaxAge == 0 {
		o.MaxAge = max(3*o.PollInterval, 2*o.QueryTimeout)
	}
	if o.Sustain == 0 {
		o.Sustain = max(30*time.Second, o.PollInterval)
	}
	if o.DueThreshold < 0 || o.StalledThreshold < 0 || o.DueThreshold == 0 && o.StalledThreshold == 0 || o.DueThreshold > int64(o.Health.ScanLimit) || o.StalledThreshold > int64(o.Health.ScanLimit) || o.PollInterval < 100*time.Millisecond || o.PollInterval > time.Minute || o.QueryTimeout < 10*time.Millisecond || o.QueryTimeout > 30*time.Second || o.MaxAge < 2*o.PollInterval || o.MaxAge < o.QueryTimeout || o.MaxAge > 5*time.Minute || o.Sustain < o.PollInterval || o.Sustain > 24*time.Hour {
		return o, ErrInvalidEventConsumerRequest
	}
	return o, nil
}

// WatchEventAcknowledgmentIntentHealth polls metadata sequentially until cancellation.
// The caller owns process lifetime and the synchronous local notification callback.
// Reads that fail, overflow, repeat, regress or become stale reset confirmation.
func WatchEventAcknowledgmentIntentHealth(ctx context.Context, db *sql.DB, accountID, appID string, options EventAcknowledgmentIntentWatchOptions, notify func(EventAcknowledgmentIntentAlert)) error {
	if ctx == nil || db == nil || notify == nil {
		return ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return err
	}
	options, err = eventIntentWatchOptions(options)
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
	var previousObserved, previousPoll time.Time
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			callCtx, cancel := context.WithTimeout(ctx, options.QueryTimeout)
			health, readErr := GetEventAcknowledgmentIntentHealth(callCtx, db, account, app, options.Health)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			now := time.Now()
			reason := eventIntentWatchEvidence(health, readErr, previousObserved, previousPoll, now, options.MaxAge)
			previousPoll = now
			if readErr == nil && !health.ObservedAt.IsZero() && !health.ObservedAt.After(now) && now.Sub(health.ObservedAt) <= options.MaxAge && health.ObservedAt.After(previousObserved) {
				previousObserved = health.ObservedAt
			}
			for i := range conditions {
				value := health.Due
				if conditions[i].metric == "stalled_recovery" {
					value = health.Stalled
				}
				var snapshot *EventAcknowledgmentIntentHealth
				if readErr == nil {
					copy := health
					snapshot = &copy
				}
				emit := func(change, why string) {
					notify(EventAcknowledgmentIntentAlert{AccountID: account, AppID: app, Metric: conditions[i].metric, Change: change, Reason: why, At: now, Value: value, Threshold: conditions[i].threshold, Health: snapshot})
				}
				conditions[i].update(value, reason, now, options.Sustain, emit)
			}
			timer.Reset(options.PollInterval)
		}
	}
}

// eventRecoveryHealthObservation contains only the evidence needed by both watches.
type eventRecoveryHealthObservation struct {
	ObservedAt  time.Time
	Complete    bool
	Truncated   bool
	InvalidRows int64
}

func eventIntentWatchEvidence(h EventAcknowledgmentIntentHealth, err error, previousObserved, previousPoll, now time.Time, maxAge time.Duration) string {
	return eventRecoveryHealthEvidence(eventRecoveryHealthObservation{ObservedAt: h.ObservedAt, Complete: h.Complete, Truncated: h.Truncated, InvalidRows: h.InvalidRows}, err, previousObserved, previousPoll, now, maxAge)
}
func eventRecoveryHealthEvidence(h eventRecoveryHealthObservation, err error, previousObserved, previousPoll, now time.Time, maxAge time.Duration) string {
	if err != nil {
		return "read_failed"
	}
	if !h.Complete || h.Truncated || h.InvalidRows != 0 {
		return "incomplete"
	}
	if h.ObservedAt.IsZero() || h.ObservedAt.After(now) || now.Sub(h.ObservedAt) > maxAge {
		return "stale"
	}
	if !previousObserved.IsZero() && !h.ObservedAt.After(previousObserved) {
		return "out_of_order"
	}
	if !previousPoll.IsZero() && now.Sub(previousPoll) > maxAge {
		return "sample_gap"
	}
	return ""
}
func (c *eventIntentWatchCondition) update(value int64, reason string, now time.Time, sustain time.Duration, emit func(string, string)) {
	if reason != "" {
		change := "unknown"
		if c.state == "firing" {
			change = "confirmation_lost"
		}
		if c.state != "unknown" || c.reason != reason {
			emit(change, reason)
		}
		c.state, c.reason = "unknown", reason
		c.first = time.Time{}
		c.samples = 0
		c.clearing = false
		return
	}
	c.reason = ""
	breach := value >= c.threshold
	if c.state == "firing" {
		if breach {
			c.first = time.Time{}
			c.samples = 0
			c.clearing = false
			return
		}
		if !c.clearing {
			c.first = now
			c.samples = 1
			c.clearing = true
			return
		}
		c.samples++
		if c.samples >= 2 && now.Sub(c.first) >= sustain {
			emit("resolved", "threshold_cleared")
			c.state = "clear"
			c.first = time.Time{}
			c.samples = 0
			c.clearing = false
		}
		return
	}
	if !breach {
		c.state = "clear"
		c.first = time.Time{}
		c.samples = 0
		return
	}
	if c.state != "pending" {
		c.state = "pending"
		c.first = now
		c.samples = 1
		return
	}
	c.samples++
	if c.samples >= 2 && now.Sub(c.first) >= sustain {
		emit("firing", "threshold_exceeded")
		c.state = "firing"
		c.first = time.Time{}
		c.samples = 0
	}
}
