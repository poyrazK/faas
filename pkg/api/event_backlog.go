package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const EventBacklogCoverage = "captured_application_recipients"

type EventBacklogFilters struct {
	App            string `json:"app,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	State          string `json:"state,omitempty"`
	CapacityScope  string `json:"capacity_scope,omitempty"`
	MinAgeSeconds  int64  `json:"min_age_seconds,omitempty"`
}

func (f EventBacklogFilters) Validate() error {
	if f.State != "" && f.State != "pending" && f.State != "processing" {
		return fmt.Errorf("state must be pending or processing")
	}
	if f.CapacityScope != "" && f.CapacityScope != "consumer" && f.CapacityScope != "app" && f.CapacityScope != "account" {
		return fmt.Errorf("capacity_scope must be consumer, app or account")
	}
	if len(f.App) > EventBacklogFilterMaxBytes || len(f.SubscriptionID) > EventBacklogFilterMaxBytes {
		return fmt.Errorf("app and subscription_id must be at most %d bytes", EventBacklogFilterMaxBytes)
	}
	if f.MinAgeSeconds < 0 || f.MinAgeSeconds > EventBacklogMinAgeMaxSeconds {
		return fmt.Errorf("min_age_seconds must be between 0 and %d", EventBacklogMinAgeMaxSeconds)
	}
	return nil
}

type EventBacklogOptions struct {
	EventBacklogFilters
	After, ConsumersAfter string
	Limit, ConsumerLimit  int
}

func (c *Client) GetEventBacklog(ctx context.Context, options EventBacklogOptions) (EventBacklogResponse, error) {
	query := url.Values{}
	for key, value := range map[string]string{"app": options.App, "subscription_id": options.SubscriptionID, "state": options.State, "capacity_scope": options.CapacityScope, "after": options.After, "consumers_after": options.ConsumersAfter} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if options.MinAgeSeconds != 0 {
		query.Set("min_age_seconds", strconv.FormatInt(options.MinAgeSeconds, 10))
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.ConsumerLimit > 0 {
		query.Set("consumer_limit", strconv.Itoa(options.ConsumerLimit))
	}
	var out EventBacklogResponse
	err := c.do(ctx, http.MethodGet, "/v1/events/backlog?"+query.Encode(), nil, &out)
	return out, err
}
