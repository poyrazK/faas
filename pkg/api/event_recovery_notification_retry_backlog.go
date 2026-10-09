package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

type EventRecoveryNotificationRetryBacklogRequest struct {
	JobID            string                                        `json:"job_id"`
	JobCreatedAt     time.Time                                     `json:"job_created_at"`
	Summary          EventRecoveryNotificationRetryDecisionSummary `json:"summary"`
	DetailPath       string                                        `json:"detail_path"`
	RetryPreviewPath string                                        `json:"retry_preview_path"`
}
type EventRecoveryNotificationRetryBacklog struct {
	AppID        string                                         `json:"app_id"`
	ObservedAt   time.Time                                      `json:"observed_at"`
	JobsScanned  int                                            `json:"jobs_scanned"`
	CountsScope  string                                         `json:"counts_scope"`
	Totals       EventRecoveryNotificationRetryHistoryTotals    `json:"totals"`
	MatchedCount int                                            `json:"matched_count"`
	Requests     []EventRecoveryNotificationRetryBacklogRequest `json:"requests"`
	NextCursor   string                                         `json:"next_cursor,omitempty"`
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
