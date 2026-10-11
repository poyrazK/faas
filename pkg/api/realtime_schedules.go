package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

type ManagedRealtimeScheduleRequest struct {
	Group              string            `json:"group,omitempty"`
	Conditions         json.RawMessage   `json:"conditions,omitempty"`
	OnConditionFailure string            `json:"on_condition_failure,omitempty"`
	IntervalSeconds    int               `json:"interval_seconds,omitempty"`
	MaxOccurrences     int64             `json:"max_occurrences,omitempty"`
	EndAt              *time.Time        `json:"end_at,omitempty"`
	MaxAttempts        int               `json:"max_attempts,omitempty"`
	BackoffSeconds     int               `json:"backoff_seconds,omitempty"`
	DataBase64         string            `json:"data_base64"`
	Binary             bool              `json:"binary"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	DeliverAt          time.Time         `json:"deliver_at"`
}
type ManagedRealtimeScheduleUpdate struct {
	DeliverAt       time.Time `json:"deliver_at"`
	ExpectedVersion int64     `json:"expected_version"`
}
type ManagedRealtimeScheduleResponse struct {
	Group                string            `json:"group,omitempty"`
	SkippedOccurrences   int64             `json:"skipped_occurrences"`
	SkipReason           string            `json:"skip_reason,omitempty"`
	Conditions           json.RawMessage   `json:"conditions,omitempty"`
	OnConditionFailure   string            `json:"on_condition_failure,omitempty"`
	IntervalSeconds      int               `json:"interval_seconds,omitempty"`
	MaxOccurrences       int64             `json:"max_occurrences,omitempty"`
	EndAt                *time.Time        `json:"end_at,omitempty"`
	InitialDeliverAt     time.Time         `json:"initial_deliver_at"`
	Occurrence           int64             `json:"occurrence"`
	CompletedOccurrences int64             `json:"completed_occurrences"`
	MaxAttempts          int               `json:"max_attempts"`
	BackoffSeconds       int               `json:"backoff_seconds"`
	Attempts             int64             `json:"attempts"`
	CycleAttempts        int               `json:"cycle_attempts"`
	NextAttemptAt        *time.Time        `json:"next_attempt_at,omitempty"`
	LastAttemptAt        *time.Time        `json:"last_attempt_at,omitempty"`
	ScheduleID           string            `json:"schedule_id"`
	Channel              string            `json:"channel"`
	DataBase64           string            `json:"data_base64"`
	Binary               bool              `json:"binary"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	DeliverAt            time.Time         `json:"deliver_at"`
	Version              int64             `json:"version"`
	Status               string            `json:"status"`
	Sequence             int64             `json:"sequence,omitempty"`
	LastError            string            `json:"last_error,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}
type ManagedRealtimeSchedulesResponse struct {
	Totals    ManagedRealtimeScheduleTotals     `json:"totals"`
	Schedules []ManagedRealtimeScheduleResponse `json:"schedules"`
}

func realtimeSchedulesAPIPath(slug, ep, ch string) string {
	return fmt.Sprintf("/v1/apps/%s/realtime/endpoints/%s/channels/%s/schedules", url.PathEscape(slug), url.PathEscape(ep), url.PathEscape(ch))
}
func (c *Client) PutManagedRealtimeSchedule(ctx context.Context, slug, ep, ch, id string, req ManagedRealtimeScheduleRequest) (ManagedRealtimeScheduleResponse, error) {
	var out ManagedRealtimeScheduleResponse
	err := c.do(ctx, "PUT", realtimeSchedulesAPIPath(slug, ep, ch)+"/"+url.PathEscape(id), req, &out)
	return out, err
}
func (c *Client) ListManagedRealtimeSchedules(ctx context.Context, slug, ep, ch string) (ManagedRealtimeSchedulesResponse, error) {
	var out ManagedRealtimeSchedulesResponse
	err := c.do(ctx, "GET", realtimeSchedulesAPIPath(slug, ep, ch), nil, &out)
	return out, err
}
func (c *Client) RescheduleManagedRealtimeSchedule(ctx context.Context, slug, ep, ch, id string, req ManagedRealtimeScheduleUpdate) (ManagedRealtimeScheduleResponse, error) {
	var out ManagedRealtimeScheduleResponse
	err := c.do(ctx, "PATCH", realtimeSchedulesAPIPath(slug, ep, ch)+"/"+url.PathEscape(id), req, &out)
	return out, err
}
func (c *Client) CancelManagedRealtimeSchedule(ctx context.Context, slug, ep, ch, id string, version int64) (ManagedRealtimeScheduleResponse, error) {
	var out ManagedRealtimeScheduleResponse
	err := c.do(ctx, "DELETE", realtimeSchedulesAPIPath(slug, ep, ch)+"/"+url.PathEscape(id)+"?expected_version="+strconv.FormatInt(version, 10), nil, &out)
	return out, err
}

// Omit DeliverAt to retry on the next worker pass.
type ManagedRealtimeScheduleRetryRequest struct {
	ExpectedVersion int64      `json:"expected_version"`
	DeliverAt       *time.Time `json:"deliver_at,omitempty"`
}

func (c *Client) RetryManagedRealtimeSchedule(ctx context.Context, slug, ep, ch, id string, req ManagedRealtimeScheduleRetryRequest) (ManagedRealtimeScheduleResponse, error) {
	var out ManagedRealtimeScheduleResponse
	err := c.do(ctx, "POST", realtimeSchedulesAPIPath(slug, ep, ch)+"/"+url.PathEscape(id)+"/retry", req, &out)
	return out, err
}

type ManagedRealtimeScheduleHistoryEvent struct {
	SkippedOccurrences   int64      `json:"skipped_occurrences"`
	SkipReason           string     `json:"skip_reason,omitempty"`
	Occurrence           int64      `json:"occurrence"`
	CompletedOccurrences int64      `json:"completed_occurrences"`
	Version              int64      `json:"version"`
	Event                string     `json:"event"`
	Status               string     `json:"status"`
	Attempts             int64      `json:"attempts"`
	CycleAttempts        int        `json:"cycle_attempts"`
	DeliverAt            time.Time  `json:"deliver_at"`
	NextAttemptAt        *time.Time `json:"next_attempt_at,omitempty"`
	FailureCode          string     `json:"failure_code,omitempty"`
	Sequence             int64      `json:"sequence,omitempty"`
	OccurredAt           time.Time  `json:"occurred_at"`
}
type ManagedRealtimeScheduleHistoryResponse struct {
	ScheduleID       string                                `json:"schedule_id"`
	Channel          string                                `json:"channel"`
	OldestVersion    int64                                 `json:"oldest_version"`
	LatestVersion    int64                                 `json:"latest_version"`
	HistoryTruncated bool                                  `json:"history_truncated"`
	HasMore          bool                                  `json:"has_more"`
	Events           []ManagedRealtimeScheduleHistoryEvent `json:"events"`
}

func (c *Client) GetManagedRealtimeScheduleHistory(ctx context.Context, slug, ep, ch, id string, after int64, limit int) (ManagedRealtimeScheduleHistoryResponse, error) {
	var out ManagedRealtimeScheduleHistoryResponse
	query := url.Values{"after_version": {strconv.FormatInt(after, 10)}}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	err := c.do(ctx, "GET", realtimeSchedulesAPIPath(slug, ep, ch)+"/"+url.PathEscape(id)+"/history?"+query.Encode(), nil, &out)
	return out, err
}

type ManagedRealtimeSchedulePauseRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func (c *Client) SetManagedRealtimeSchedulePaused(ctx context.Context, slug, ep, ch, id string, version int64, pause bool) (ManagedRealtimeScheduleResponse, error) {
	action := "resume"
	if pause {
		action = "pause"
	}
	var out ManagedRealtimeScheduleResponse
	err := c.do(ctx, "POST", realtimeSchedulesAPIPath(slug, ep, ch)+"/"+url.PathEscape(id)+"/"+action, ManagedRealtimeSchedulePauseRequest{version}, &out)
	return out, err
}

// RealtimeScheduleCompletionWebhookPayload is the data in a signed app webhook
// envelope for a committed publication, terminal failure, or intentional skip.
type RealtimeScheduleCompletionWebhookPayload struct {
	EventID              string    `json:"event_id"`
	AppID                string    `json:"app_id"`
	EndpointID           string    `json:"endpoint_id"`
	Channel              string    `json:"channel"`
	ScheduleID           string    `json:"schedule_id"`
	Version              int64     `json:"version"`
	Occurrence           int64     `json:"occurrence"`
	CompletedOccurrences int64     `json:"completed_occurrences"`
	SkippedOccurrences   int64     `json:"skipped_occurrences"`
	Outcome              string    `json:"outcome"`
	Attempts             int64     `json:"attempts"`
	CycleAttempts        int       `json:"cycle_attempts"`
	DeliverAt            time.Time `json:"deliver_at"`
	OccurredAt           time.Time `json:"occurred_at"`
	Sequence             int64     `json:"sequence,omitempty"`
	FailureCode          string    `json:"failure_code,omitempty"`
	SkipReason           string    `json:"skip_reason,omitempty"`
}

// Totals describe the retained schedules matching the list filters.
type ManagedRealtimeScheduleTotals struct {
	Pending              int   `json:"pending"`
	Paused               int   `json:"paused"`
	Published            int   `json:"published"`
	Failed               int   `json:"failed"`
	Skipped              int   `json:"skipped"`
	Canceled             int   `json:"canceled"`
	CompletedOccurrences int64 `json:"completed_occurrences"`
	SkippedOccurrences   int64 `json:"skipped_occurrences"`
}
type ManagedRealtimeScheduleGroupRequest struct {
	ExpectedVersions map[string]int64 `json:"expected_versions"`
}

func (c *Client) ListManagedRealtimeSchedulesFiltered(ctx context.Context, slug, ep, ch string, group, status string) (ManagedRealtimeSchedulesResponse, error) {
	query := url.Values{}
	if group != "" {
		query.Set("group", group)
	}
	if status != "" {
		query.Set("status", status)
	}
	var out ManagedRealtimeSchedulesResponse
	err := c.do(ctx, "GET", realtimeSchedulesAPIPath(slug, ep, ch)+"?"+query.Encode(), nil, &out)
	return out, err
}
func (c *Client) ApplyManagedRealtimeScheduleGroup(ctx context.Context, slug, ep, ch, group, action string, req ManagedRealtimeScheduleGroupRequest) (ManagedRealtimeSchedulesResponse, error) {
	if action != "pause" && action != "resume" && action != "cancel" {
		return ManagedRealtimeSchedulesResponse{}, fmt.Errorf("invalid schedule group action")
	}
	var out ManagedRealtimeSchedulesResponse
	err := c.do(ctx, "POST", realtimeSchedulesAPIPath(slug, ep, ch)+"/groups/"+url.PathEscape(group)+"/"+action, req, &out)
	return out, err
}
