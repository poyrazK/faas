package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

type EventSubscriptionCapacityGuard struct {
	MaxPendingDeliveries     *int `json:"max_pending_deliveries"`
	PlanMaxPendingDeliveries *int `json:"plan_max_pending_deliveries"`
}

type EventSubscriptionCapacityRequest struct {
	Expected             *EventSubscriptionCapacityGuard `json:"expected,omitempty"`
	MaxPendingDeliveries *int                            `json:"max_pending_deliveries"`
}

func (r EventSubscriptionCapacityRequest) Validate() error {
	if r.MaxPendingDeliveries == nil || *r.MaxPendingDeliveries < 0 {
		return fmt.Errorf("max_pending_deliveries must be explicit and nonnegative")
	}
	if e := r.Expected; e != nil && (e.MaxPendingDeliveries == nil || e.PlanMaxPendingDeliveries == nil || *e.MaxPendingDeliveries < 0 || *e.PlanMaxPendingDeliveries <= 0) {
		return fmt.Errorf("expected requires a nonnegative saved setting and a positive plan ceiling")
	}
	return nil
}

func (c *Client) SetEventSubscriptionCapacity(ctx context.Context, app, id string, req EventSubscriptionCapacityRequest) (EventSubscriptionDeliveryControl, error) {
	var out EventSubscriptionDeliveryControl
	err := c.do(ctx, http.MethodPut, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(id)+"/delivery-control"+"/capacity", req, &out)
	return out, err
}
