package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type EventRecoveryNotificationRetryBacklogQuery struct {
	Status   string `json:"status,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}

func (q *EventRecoveryNotificationRetryBacklogQuery) Normalize() error {
	if q.Status == "" {
		q.Status = "failed,pending,inconclusive"
	}
	statuses, err := ParseEventRecoveryNotificationRetryHistoryStatus(q.Status)
	if err != nil {
		return err
	}
	q.Status = strings.Join(statuses, ",")
	if q.PageSize == 0 {
		q.PageSize = EventRecoveryNotificationRetryBacklogJobsDefault
	}
	if q.PageSize < 1 || q.PageSize > EventRecoveryNotificationRetryBacklogJobsMax || len(q.Cursor) > EventRecoveryCursorMaxBytes {
		return fmt.Errorf("invalid retry backlog page size or cursor")
	}
	return nil
}

func (c *Client) ListEventRecoveryNotificationRetryBacklog(ctx context.Context, app string, query EventRecoveryNotificationRetryBacklogQuery) (EventRecoveryNotificationRetryBacklog, error) {
	var out EventRecoveryNotificationRetryBacklog
	if err := query.Normalize(); err != nil {
		return out, err
	}
	values := url.Values{"status": {query.Status}, "page_size": {strconv.Itoa(query.PageSize)}}
	if query.Cursor != "" {
		values.Set("cursor", query.Cursor)
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-recoveries/notification-retry-backlog?"+values.Encode(), nil, &out)
	return out, err
}
