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
	StartedAt     time.Time                    `json:"started_at"`
	ObservedAt    time.Time                    `json:"observed_at"`
	Routing       EventConsumerHealth          `json:"routing"`
	Execution     EventConsumerExecutionHealth `json:"execution"`
	CapacityWaits *EventConsumerCapacityWaits  `json:"capacity_waits,omitempty"`
	PendingSample EventConsumerPendingSample   `json:"pending_sample"`
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
