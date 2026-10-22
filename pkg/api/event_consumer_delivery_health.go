package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// EventConsumerDeliveryHealth combines independent routing, execution and
// oldest-first pending reads. It is not a single database snapshot.
type EventConsumerDeliveryHealth struct {
	Processing       *EventConsumerProcessingHealth `json:"processing,omitempty"`
	CapacityPressure *EventConsumerCapacityPressure `json:"capacity_pressure,omitempty"`
	StartedAt        time.Time                      `json:"started_at"`
	ObservedAt       time.Time                      `json:"observed_at"`
	Routing          EventConsumerHealth            `json:"routing"`
	Execution        EventConsumerExecutionHealth   `json:"execution"`
	WaitingReasons   *EventConsumerWaitingReasons   `json:"waiting_reasons,omitempty"`
	CapacityWaits    *EventConsumerCapacityWaits    `json:"capacity_waits,omitempty"`
	PendingSample    EventConsumerPendingSample     `json:"pending_sample"`
}

// EventConsumerWaitingReasons partitions the complete pending backlog by its
// effective waiting reason, independently of the investigative sample.
type EventConsumerWaitingReasons struct {
	AppID             string           `json:"app_id"`
	SubscriptionID    string           `json:"subscription_id"`
	ObservedAt        time.Time        `json:"observed_at"`
	WindowAt          time.Time        `json:"window_at"`
	Coverage          string           `json:"coverage"`
	Complete          bool             `json:"complete"`
	PendingRecipients int64            `json:"pending_recipients"`
	Counts            map[string]int64 `json:"counts"`
}

// NewEventPendingWaitingReasonCounts includes zero counts for each known reason.
func NewEventPendingWaitingReasonCounts() map[string]int64 {
	return map[string]int64{
		"circuit_open":                  0,
		"circuit_probe_wait":            0,
		"circuit_recovery_rate_limited": 0,
		"subscription_paused":           0,
		"subscription_rate_limited":     0,
		"ordering_blocked":              0,
		"capacity_consumer":             0,
		"capacity_app":                  0,
		"capacity_account":              0,
		"routing_in_progress":           0,
		"receipt_processing":            0,
		"retry_backoff":                 0,
		"workflow_routing":              0,
		"ready":                         0,
	}
}

// EventPendingWaitingReasonCountsComplete checks a nonnegative exact partition.
func EventPendingWaitingReasonCountsComplete(counts map[string]int64, pending int64) bool {
	known := NewEventPendingWaitingReasonCounts()
	if pending < 0 || len(counts) != len(known) {
		return false
	}
	remaining := pending
	for reason, count := range counts {
		if _, ok := known[reason]; !ok || count < 0 || count > remaining {
			return false
		}
		remaining -= count
	}
	return remaining == 0
}

// EventConsumerCapacityWaits counts effective capacity waiting reasons across
// all retained pending application recipients in one backlog snapshot.
type EventConsumerCapacityWaits struct {
	AppID             string    `json:"app_id"`
	SubscriptionID    string    `json:"subscription_id"`
	ObservedAt        time.Time `json:"observed_at"`
	Coverage          string    `json:"coverage"`
	Complete          bool      `json:"complete"`
	PendingRecipients int64     `json:"pending_recipients"`
	Consumer          int64     `json:"consumer"`
	App               int64     `json:"app"`
	Account           int64     `json:"account"`
}

// Current admission pressure and effective recorded waiting reasons share one
// snapshot. Oldest age is since acceptance, not continuous capacity-wait duration.
type EventConsumerCapacityPressure struct {
	CapacityCommandBudget          *EventSubscriptionCapacityCommandBudget `json:"capacity_command_budget,omitempty"`
	PlanMaxPendingDeliveries       int                                     `json:"plan_max_pending_deliveries"`
	AppID                          string                                  `json:"app_id"`
	SubscriptionID                 string                                  `json:"subscription_id"`
	ObservedAt                     time.Time                               `json:"observed_at"`
	Coverage                       string                                  `json:"coverage"`
	Complete                       bool                                    `json:"complete"`
	BlockingScope                  string                                  `json:"blocking_scope"`
	ConfiguredMaxPendingDeliveries int                                     `json:"configured_max_pending_deliveries"`
	Scopes                         []EventConsumerCapacityScope            `json:"scopes"`
}

type EventConsumerCapacityScope struct {
	Scope                   string     `json:"scope"`
	LiveDeliveries          int64      `json:"live_deliveries"`
	EffectiveLimit          int        `json:"effective_limit"`
	AtLimit                 bool       `json:"at_limit"`
	WaitingRecipients       int64      `json:"waiting_recipients"`
	OldestWaitingAcceptedAt *time.Time `json:"oldest_waiting_accepted_at,omitempty"`
	OldestWaitingAgeSeconds float64    `json:"oldest_waiting_age_seconds"`
	Advice                  string     `json:"advice"`
}

// WaitingReasons counts sampled pending recipients, never the whole backlog.
type EventConsumerPendingSample struct {
	ObservedAt     time.Time               `json:"observed_at"`
	Coverage       string                  `json:"coverage"`
	Truncated      bool                    `json:"truncated"`
	WaitingReasons map[string]int64        `json:"waiting_reasons"`
	Recipients     []EventBacklogRecipient `json:"recipients"`
}

func (c *Client) GetEventConsumerDeliveryHealth(ctx context.Context, app, id, window string) (EventConsumerDeliveryHealth, error) {
	var out EventConsumerDeliveryHealth
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(id)+"/delivery-health?window="+url.QueryEscape(window), nil, &out)
	return out, err
}
