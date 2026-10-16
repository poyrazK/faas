package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type RescheduleEnvironmentCacheRequest struct {
	ReplacementID     string    `json:"replacement_id"`
	AppID             string    `json:"app_id"`
	ProjectID         string    `json:"project_id"`
	EnvironmentID     string    `json:"environment_id"`
	ExpectedExecuteAt time.Time `json:"expected_execute_at"`
	ExecuteAt         time.Time `json:"execute_at"`
}
type EnvironmentCacheScheduleListOptions struct {
	Status string     `json:"status,omitempty"`
	From   *time.Time `json:"from,omitempty"`
	Until  *time.Time `json:"until,omitempty"`
	After  string     `json:"after,omitempty"`
	Limit  int        `json:"limit,omitempty"`
}
type EnvironmentCacheScheduleList struct {
	AppID         string                     `json:"app_id"`
	ProjectID     string                     `json:"project_id"`
	EnvironmentID string                     `json:"environment_id"`
	Scope         string                     `json:"scope"`
	Entries       []EnvironmentCacheSchedule `json:"entries"`
	NextCursor    string                     `json:"next_cursor,omitempty"`
}

// Cursor metadata contains no action bodies. It is scope/filter-bound, not an authorization grant.
type EnvironmentCacheScheduleCursor struct {
	AppID         string     `json:"app"`
	ProjectID     string     `json:"project"`
	EnvironmentID string     `json:"environment"`
	Status        string     `json:"status"`
	From          *time.Time `json:"from,omitempty"`
	Until         *time.Time `json:"until,omitempty"`
	ExecuteAt     time.Time  `json:"at"`
	ScheduleID    string     `json:"id"`
}

func NormalizeEnvironmentCacheScheduleList(o EnvironmentCacheScheduleListOptions) (EnvironmentCacheScheduleListOptions, error) {
	if o.Limit == 0 {
		o.Limit = EnvironmentCacheSchedulePageDefault
	}
	if o.Limit < 1 || o.Limit > EnvironmentCacheSchedulePageMax || len(o.After) > EnvironmentCacheScheduleCursorMaxBytes {
		return o, errors.New("invalid schedule page size or cursor")
	}
	switch o.Status {
	case "", "scheduled", "executing", "executed", "failed", "cancelled":
	default:
		return o, errors.New("unknown schedule status")
	}
	normalize := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		v := t.UTC().Truncate(time.Microsecond)
		return &v
	}
	o.From = normalize(o.From)
	o.Until = normalize(o.Until)
	if o.From != nil && o.From.IsZero() || o.Until != nil && o.Until.IsZero() || o.From != nil && o.Until != nil && !o.From.Before(*o.Until) {
		return o, errors.New("schedule time range must have from before until")
	}
	return o, nil
}
func ParseEnvironmentCacheScheduleCursor(o EnvironmentCacheScheduleListOptions, app, project, env string) (EnvironmentCacheScheduleCursor, error) {
	out := EnvironmentCacheScheduleCursor{AppID: app, ProjectID: project, EnvironmentID: env, Status: o.Status, From: o.From, Until: o.Until}
	if o.After == "" {
		return out, nil
	}
	if len(o.After) > EnvironmentCacheScheduleCursorMaxBytes {
		return out, errors.New("schedule cursor is too large")
	}
	bad := errors.New("schedule cursor does not match target or filters")
	raw, err := base64.RawURLEncoding.DecodeString(o.After)
	if err != nil {
		return out, bad
	}
	var c EnvironmentCacheScheduleCursor
	if json.Unmarshal(raw, &c) != nil {
		return out, bad
	}
	equal := func(a, b *time.Time) bool { return a == nil && b == nil || a != nil && b != nil && a.Equal(*b) }
	if c.AppID != app || c.ProjectID != project || c.EnvironmentID != env || c.Status != o.Status || !equal(c.From, o.From) || !equal(c.Until, o.Until) || c.ExecuteAt.IsZero() {
		return out, bad
	}
	if _, err = uuid.Parse(c.ScheduleID); err != nil {
		return out, bad
	}
	return c, nil
}
func EncodeEnvironmentCacheScheduleCursor(c EnvironmentCacheScheduleCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func ValidateRescheduleEnvironmentCache(id string, req RescheduleEnvironmentCacheRequest) error {
	for _, v := range []string{id, req.ReplacementID, req.AppID, req.ProjectID, req.EnvironmentID} {
		if _, err := uuid.Parse(v); err != nil {
			return errors.New("reschedule requires exact schedule and target UUIDs")
		}
	}
	if req.ReplacementID == id || req.ExpectedExecuteAt.IsZero() || req.ExecuteAt.IsZero() || req.ExpectedExecuteAt.UTC().Truncate(time.Microsecond).Equal(req.ExecuteAt.UTC().Truncate(time.Microsecond)) {
		return errors.New("select a different replacement UUID and execution time")
	}
	return nil
}
func (c *Client) ListEnvironmentCacheSchedules(ctx context.Context, slug, env string, o EnvironmentCacheScheduleListOptions) (EnvironmentCacheScheduleList, error) {
	var out EnvironmentCacheScheduleList
	var err error
	o, err = NormalizeEnvironmentCacheScheduleList(o)
	if err != nil {
		return out, err
	}
	q := url.Values{"status": {o.Status}, "after": {o.After}, "limit": {strconv.Itoa(o.Limit)}}
	if o.From != nil {
		q.Set("from", o.From.Format(time.RFC3339Nano))
	}
	if o.Until != nil {
		q.Set("until", o.Until.Format(time.RFC3339Nano))
	}
	err = c.do(ctx, "GET", cacheSchedulePath(slug, env, "")+"&"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) RescheduleEnvironmentCache(ctx context.Context, slug, env, id string, req RescheduleEnvironmentCacheRequest) (EnvironmentCacheSchedule, error) {
	var out EnvironmentCacheSchedule
	err := c.doWithIdempotencyKey(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/cache/schedules/"+url.PathEscape(id)+"/reschedule?environment="+url.QueryEscape(env), req, &out, req.ReplacementID)
	return out, err
}

func EnvironmentCacheScheduleReplacement(before EnvironmentCacheSchedule, id string, at time.Time) (CreateEnvironmentCacheScheduleRequest, error) {
	raw, err := json.Marshal(before.CreateEnvironmentCacheScheduleRequest)
	if err != nil {
		return CreateEnvironmentCacheScheduleRequest{}, err
	}
	var out CreateEnvironmentCacheScheduleRequest
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	out.ScheduleID = id
	out.ExecuteAt = at.UTC().Truncate(time.Microsecond)
	if out.Policy != nil {
		out.Policy.RequestID = id
	}
	if out.Purge != nil {
		out.Purge.RequestID = id
	}
	return out, ValidateEnvironmentCacheSchedule(out)
}
