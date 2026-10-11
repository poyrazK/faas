package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

type EventRecoveryControlRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (r EventRecoveryControlRequest) Validate() error {
	if !utf8.ValidString(r.Reason) || len(r.Reason) > EventRecoveryReasonMaxBytes {
		return fmt.Errorf("reason must be valid UTF-8 and at most %d bytes", EventRecoveryReasonMaxBytes)
	}
	for _, ch := range r.Reason {
		if unicode.IsControl(ch) {
			return fmt.Errorf("reason must not contain control characters")
		}
	}
	return nil
}

type EventRecoveryHistoryEntry struct {
	ID            int64     `json:"id"`
	OccurredAt    time.Time `json:"occurred_at"`
	Action        string    `json:"action"`
	ActorKind     string    `json:"actor_kind"`
	ActorID       string    `json:"actor_id"`
	Reason        string    `json:"reason,omitempty"`
	PreviousState string    `json:"previous_state"`
	State         string    `json:"state"`
	PreviousRate  int       `json:"previous_rate"`
	Rate          int       `json:"rate"`
}
type EventRecoveryHistory struct {
	JobID     string                      `json:"job_id"`
	Entries   []EventRecoveryHistoryEntry `json:"entries"`
	NextAfter int64                       `json:"next_after,omitempty"`
}

func (c *Client) ListEventRecoveryHistory(ctx context.Context, id string, after int64, limit int) (EventRecoveryHistory, error) {
	var out EventRecoveryHistory
	v := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(id)+"/history?"+v.Encode(), nil, &out)
	return out, err
}
func (c *Client) PauseEventRecoveryWithReason(ctx context.Context, id string, req EventRecoveryControlRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/pause", req, &out)
	return out, err
}
func (c *Client) ResumeEventRecoveryWithReason(ctx context.Context, id string, req EventRecoveryControlRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/resume", req, &out)
	return out, err
}
func (c *Client) CancelEventRecoveryWithReason(ctx context.Context, id string, req EventRecoveryControlRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/cancel", req, &out)
	return out, err
}
