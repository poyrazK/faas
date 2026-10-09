package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type EventRecoveryListQuery struct {
	State          string     `json:"state,omitempty"`
	Mode           string     `json:"mode,omitempty"`
	SubscriptionID string     `json:"subscription_id,omitempty"`
	CreatedAfter   *time.Time `json:"created_after,omitempty"`
	CreatedBefore  *time.Time `json:"created_before,omitempty"`
	Cursor         string     `json:"cursor,omitempty"`
	Limit          int        `json:"limit,omitempty"`
}

func (q EventRecoveryListQuery) Validate() error {
	switch q.State {
	case "", "running", "paused", "completed", "cancelled":
	default:
		return fmt.Errorf("invalid recovery state")
	}
	if q.Mode != "" && q.Mode != "routing" && q.Mode != "execution" {
		return fmt.Errorf("invalid recovery mode")
	}
	if len(q.SubscriptionID) > EventBacklogFilterMaxBytes || len(q.Cursor) > EventRecoveryCursorMaxBytes {
		return fmt.Errorf("recovery filter or cursor too long")
	}
	if q.Limit < 0 || q.Limit > EventRecoveryJobsPageMax {
		return fmt.Errorf("limit must be between 1 and %d; zero uses the default", EventRecoveryJobsPageMax)
	}
	if q.CreatedAfter != nil && q.CreatedBefore != nil && !q.CreatedAfter.Before(*q.CreatedBefore) {
		return fmt.Errorf("created_after must precede created_before")
	}
	return nil
}

type EventRecoveryJobs struct {
	Jobs       []EventRecoveryJob `json:"jobs"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func (c *Client) ListEventRecoveries(ctx context.Context, app string, q EventRecoveryListQuery) (EventRecoveryJobs, error) {
	var out EventRecoveryJobs
	if err := q.Validate(); err != nil {
		return out, err
	}
	v := url.Values{}
	for k, value := range map[string]string{"state": q.State, "mode": q.Mode, "subscription_id": q.SubscriptionID, "cursor": q.Cursor} {
		if value != "" {
			v.Set(k, value)
		}
	}
	if q.Limit != 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.CreatedAfter != nil {
		v.Set("created_after", q.CreatedAfter.Format(time.RFC3339Nano))
	}
	if q.CreatedBefore != nil {
		v.Set("created_before", q.CreatedBefore.Format(time.RFC3339Nano))
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-recoveries?"+v.Encode(), nil, &out)
	return out, err
}
