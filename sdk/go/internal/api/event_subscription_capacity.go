package api

import (
	"context"
	"net/http"
	"net/url"
)

type EventSubscriptionCapacityRequest struct {
	MaxPendingDeliveries *int `json:"max_pending_deliveries"`
}

func (c *Client) SetEventSubscriptionCapacity(ctx context.Context, app, id string, req EventSubscriptionCapacityRequest) (EventSubscriptionDeliveryControl, error) {
	var out EventSubscriptionDeliveryControl
	err := c.do(ctx, http.MethodPut, "/v1/apps/"+url.PathEscape(app)+"/event-subscriptions/"+url.PathEscape(id)+"/delivery-control"+"/capacity", req, &out)
	return out, err
}
