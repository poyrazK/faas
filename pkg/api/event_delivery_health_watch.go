package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// EventDeliveryHealthWatchOptions selects opt-in conditions. Zero disables a
// threshold. Circuit/capacity durations measure consecutive local fresh reads.
type EventDeliveryHealthWatchOptions struct {
	Window                                                 string
	Interval, Timeout, MaxAge                              time.Duration
	OldestPending, Paused, CircuitBlocked, CapacityBlocked time.Duration
	Retrying, DeadLetters                                  int64
	// WaitingReasonCounts and WaitingReasonDurations select known pending reasons.
	WaitingReasonCounts    map[string]int64
	WaitingReasonDurations map[string]time.Duration
	OnProgress             func(EventDeliveryHealthWatchResult) error
}

type EventDeliveryHealthAlert struct {
	AppID          string    `json:"app_id"`
	SubscriptionID string    `json:"subscription_id"`
	Sequence       uint64    `json:"sequence"`
	IncidentID     string    `json:"incident_id"`
	Metric         string    `json:"metric"`
	Transition     string    `json:"transition"`
	Reason         string    `json:"reason"`
	At             time.Time `json:"at"`
	Value          float64   `json:"value"`
	Threshold      float64   `json:"threshold"`
}

type EventDeliveryHealthCondition struct {
	Metric     string  `json:"metric"`
	State      string  `json:"state"`
	Value      float64 `json:"value"`
	Threshold  float64 `json:"threshold"`
	IncidentID string  `json:"incident_id,omitempty"`
}

type EventDeliveryHealthWatchResult struct {
	AppID          string                         `json:"app_id,omitempty"`
	SubscriptionID string                         `json:"subscription_id"`
	Session        string                         `json:"session"`
	Sequence       uint64                         `json:"sequence"`
	Observations   int                            `json:"observations"`
	StopReason     string                         `json:"stop_reason"`
	Health         *EventConsumerDeliveryHealth   `json:"health,omitempty"`
	Conditions     []EventDeliveryHealthCondition `json:"conditions"`
	Alerts         []EventDeliveryHealthAlert     `json:"alerts"`
}

type deliveryHealthCondition struct {
	EventDeliveryHealthCondition
	first, localLast, observedAt time.Time
	samples                      int
	sustained                    bool
}

func (o EventDeliveryHealthWatchOptions) validate() error {
	if _, err := EventConsumerHealthWindow(o.Window); err != nil {
		return ErrValidation(err.Error())
	}
	if o.Interval < EventDeliveryHealthWatchIntervalMin || o.Interval > EventDeliveryHealthWatchIntervalMax || o.Timeout <= 0 || o.Timeout > EventDeliveryHealthWatchTimeoutMax || o.MaxAge < EventDeliveryHealthWatchAgeMin || o.MaxAge > EventDeliveryHealthWatchAgeMax {
		return ErrValidation("invalid delivery health watch interval, timeout or max age")
	}
	enabled := false
	for _, d := range []time.Duration{o.OldestPending, o.Paused, o.CircuitBlocked, o.CapacityBlocked} {
		if d < 0 || d > EventDeliveryHealthWatchThresholdMax || (d > 0 && d < EventDeliveryHealthWatchThresholdMin) {
			return ErrValidation("invalid delivery health duration threshold")
		}
		enabled = enabled || d > 0
	}
	for _, n := range []int64{o.Retrying, o.DeadLetters} {
		if n < 0 || n > EventDeliveryHealthWatchCountMax {
			return ErrValidation("invalid delivery health count threshold")
		}
		enabled = enabled || n > 0
	}
	known := NewEventPendingWaitingReasonCounts()
	for reason, n := range o.WaitingReasonCounts {
		if _, ok := known[reason]; !ok || n <= 0 || n > EventDeliveryHealthWatchCountMax {
			return ErrValidation("invalid waiting reason count threshold")
		}
		enabled = true
	}
	for reason, d := range o.WaitingReasonDurations {
		if _, ok := known[reason]; !ok || d < EventDeliveryHealthWatchThresholdMin || d > EventDeliveryHealthWatchThresholdMax {
			return ErrValidation("invalid waiting reason duration threshold")
		}
		enabled = true
	}
	if !enabled {
		return ErrValidation("enable at least one delivery health threshold")
	}
	return nil
}

func newDeliveryHealthConditions(o EventDeliveryHealthWatchOptions) []*deliveryHealthCondition {
	out := []*deliveryHealthCondition{}
	for _, v := range []struct {
		metric    string
		threshold float64
		sustained bool
	}{
		{"oldest_pending_seconds", o.OldestPending.Seconds(), false},
		{"retrying_executions", float64(o.Retrying), false},
		{"dead_lettered_executions", float64(o.DeadLetters), false},
		{"paused_seconds", o.Paused.Seconds(), false},
		{"circuit_blocked_seconds", o.CircuitBlocked.Seconds(), true},
		{"capacity_blocked_seconds", o.CapacityBlocked.Seconds(), true},
	} {
		if v.threshold > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: v.metric, State: "unknown", Threshold: v.threshold}, sustained: v.sustained})
		}
	}
	reasons := make([]string, 0, len(NewEventPendingWaitingReasonCounts()))
	for reason := range NewEventPendingWaitingReasonCounts() {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		if n := o.WaitingReasonCounts[reason]; n > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "waiting_reason." + reason + ".recipients", State: "unknown", Threshold: float64(n)}})
		}
		if d := o.WaitingReasonDurations[reason]; d > 0 {
			out = append(out, &deliveryHealthCondition{EventDeliveryHealthCondition: EventDeliveryHealthCondition{Metric: "waiting_reason." + reason + ".seconds", State: "unknown", Threshold: d.Seconds()}, sustained: true})
		}
	}
	return out
}

// WatchEventConsumerDeliveryHealth emits one local notification per confirmed
// transition. Notifications are invocation-local, not a durable alert outbox.
func (c *Client) WatchEventConsumerDeliveryHealth(ctx context.Context, app, sub string, o EventDeliveryHealthWatchOptions) (EventDeliveryHealthWatchResult, error) {
	out := EventDeliveryHealthWatchResult{SubscriptionID: strings.ToLower(sub), StopReason: "validation_failure", Conditions: []EventDeliveryHealthCondition{}, Alerts: []EventDeliveryHealthAlert{}}
	if err := o.validate(); err != nil {
		return out, err
	}
	if app == "" || sub == "" {
		return out, ErrValidation("app and subscription are required")
	}
	var namespace [16]byte
	if _, err := rand.Read(namespace[:]); err != nil {
		return out, fmt.Errorf("alert namespace: %w", err)
	}
	out.Session, out.StopReason = hex.EncodeToString(namespace[:]), "running"
	conditions := newDeliveryHealthConditions(o)
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	appID := ""
	for {
		out.Alerts = []EventDeliveryHealthAlert{}
		health, err := c.GetEventConsumerDeliveryHealth(ctx, app, sub, o.Window)
		now := time.Now()
		if err != nil {
			for _, condition := range conditions {
				updateDeliveryHealthCondition(&out, condition, 0, false, time.Time{}, now, o.MaxAge)
			}
			out.StopReason = "read_failure"
			if ctx.Err() != nil {
				out.StopReason = "cancelled"
				if ctx.Err() == context.DeadlineExceeded {
					out.StopReason = "timeout"
				}
			}
			collectDeliveryHealthConditions(&out, conditions)
			if callbackErr := deliveryHealthProgress(o, out); callbackErr != nil {
				out.StopReason = "callback_failure"
				return out, callbackErr
			}
			return out, err
		}
		out.Observations++
		out.Health = &health
		binding := health.Routing.AppID != "" && strings.EqualFold(health.Routing.AppID, health.Execution.AppID) && strings.EqualFold(health.Routing.SubscriptionID, sub) && strings.EqualFold(health.Execution.SubscriptionID, sub)
		if appID == "" && binding {
			appID = health.Routing.AppID
		}
		out.AppID = appID
		binding = binding && strings.EqualFold(appID, health.Routing.AppID)
		for _, condition := range conditions {
			value, valid, observed := deliveryHealthValue(health, condition.Metric)
			updateDeliveryHealthCondition(&out, condition, value, binding && valid, observed, now, o.MaxAge)
		}
		collectDeliveryHealthConditions(&out, conditions)
		if err := deliveryHealthProgress(o, out); err != nil {
			out.StopReason = "callback_failure"
			return out, err
		}
		timer := time.NewTimer(o.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			out.Alerts = []EventDeliveryHealthAlert{}
			out.StopReason = "cancelled"
			if ctx.Err() == context.DeadlineExceeded {
				out.StopReason = "timeout"
			}
			return out, ctx.Err()
		case <-timer.C:
		}
	}
}

func collectDeliveryHealthConditions(out *EventDeliveryHealthWatchResult, conditions []*deliveryHealthCondition) {
	out.Conditions = make([]EventDeliveryHealthCondition, 0, len(conditions))
	for _, condition := range conditions {
		out.Conditions = append(out.Conditions, condition.EventDeliveryHealthCondition)
	}
}

func deliveryHealthProgress(o EventDeliveryHealthWatchOptions, out EventDeliveryHealthWatchResult) error {
	if o.OnProgress == nil {
		return nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("copy delivery health progress: %w", err)
	}
	var snapshot EventDeliveryHealthWatchResult
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("copy delivery health progress: %w", err)
	}
	return o.OnProgress(snapshot)
}

func updateDeliveryHealthCondition(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, value float64, valid bool, observed, now time.Time, maxAge time.Duration) {
	valid = valid && !observed.IsZero() && !observed.After(now) && now.Sub(observed) <= maxAge && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
	gap := !c.localLast.IsZero() && now.Sub(c.localLast) > maxAge
	monotonic := c.observedAt.IsZero() || observed.After(c.observedAt)
	if gap {
		loseDeliveryHealthConfirmation(out, c, now, "sample_gap")
	}
	if !valid || !monotonic {
		loseDeliveryHealthConfirmation(out, c, now, "evidence_unavailable")
		return
	}
	c.observedAt, c.localLast = observed, now
	if c.sustained {
		if value > 0 {
			if c.first.IsZero() {
				c.first = now
				c.samples = 0
			}
			c.samples++
			value = now.Sub(c.first).Seconds()
		} else {
			c.first = time.Time{}
			c.samples = 0
		}
	}
	c.Value = value
	breached := value >= c.Threshold && (!c.sustained || c.samples >= 2)
	if breached && c.State != "firing" {
		out.Sequence++
		c.IncidentID = fmt.Sprintf("%s-%d", out.Session, out.Sequence)
		c.State = "firing"
		appendDeliveryHealthAlert(out, c, now, "opened", "threshold_breached")
	} else if !breached {
		if c.State == "firing" {
			out.Sequence++
			appendDeliveryHealthAlert(out, c, now, "resolved", "threshold_cleared")
		}
		c.State, c.IncidentID = "clear", ""
	}
}

func loseDeliveryHealthConfirmation(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, now time.Time, reason string) {
	if c.State == "firing" {
		out.Sequence++
		appendDeliveryHealthAlert(out, c, now, "confirmation_lost", reason)
	}
	c.State, c.IncidentID, c.Value = "unknown", "", 0
	c.first, c.localLast, c.observedAt, c.samples = time.Time{}, time.Time{}, time.Time{}, 0
}

func appendDeliveryHealthAlert(out *EventDeliveryHealthWatchResult, c *deliveryHealthCondition, now time.Time, transition, reason string) {
	out.Alerts = append(out.Alerts, EventDeliveryHealthAlert{AppID: out.AppID, SubscriptionID: out.SubscriptionID, Sequence: out.Sequence, IncidentID: c.IncidentID, Metric: c.Metric, Transition: transition, Reason: reason, At: now.UTC(), Value: c.Value, Threshold: c.Threshold})
}
