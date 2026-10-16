package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"
)

// Schedule activity records committed transitions, not sampled gateway evidence.
type EnvironmentCacheScheduleEvent struct {
	Sequence          int64     `json:"sequence"`
	ScheduleID        string    `json:"schedule_id"`
	Kind              string    `json:"kind"`
	Status            string    `json:"status"`
	Action            string    `json:"action"`
	ExecuteAt         time.Time `json:"execute_at"`
	OccurredAt        time.Time `json:"occurred_at"`
	RelatedScheduleID string    `json:"related_schedule_id,omitempty"`
	ErrorCode         string    `json:"error_code,omitempty"`
	PolicyRevision    *int64    `json:"policy_revision,omitempty"`
	PurgeGeneration   *int64    `json:"purge_generation,omitempty"`
}
type EnvironmentCacheScheduleActivity struct {
	AppID         string                          `json:"app_id"`
	ProjectID     string                          `json:"project_id"`
	EnvironmentID string                          `json:"environment_id"`
	Scope         string                          `json:"scope"`
	Entries       []EnvironmentCacheScheduleEvent `json:"entries"`
	Cursor        string                          `json:"cursor"`
	HasMore       bool                            `json:"has_more"`
}

// EnvironmentCacheActivityCursor is internal continuation metadata, not a grant.
type EnvironmentCacheActivityCursor struct {
	AccountID     string `json:"account"`
	AppID         string `json:"app"`
	ProjectID     string `json:"project"`
	EnvironmentID string `json:"environment"`
	Sequence      int64  `json:"sequence"`
}

func NormalizeEnvironmentCacheActivityPage(after string, limit int) (int, error) {
	if limit == 0 {
		limit = EnvironmentCacheActivityPageDefault
	}
	if limit < 1 || limit > EnvironmentCacheActivityPageMax || len(after) > EnvironmentCacheActivityCursorMaxBytes {
		return 0, errors.New("invalid cache activity page size or cursor")
	}
	return limit, nil
}
func ParseEnvironmentCacheActivityCursor(after, account, app, project, env string) (EnvironmentCacheActivityCursor, error) {
	out := EnvironmentCacheActivityCursor{AccountID: account, AppID: app, ProjectID: project, EnvironmentID: env}
	if after == "" {
		return out, nil
	}
	bad := errors.New("activity cursor does not match this account and immutable environment")
	if len(after) > EnvironmentCacheActivityCursorMaxBytes {
		return out, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(after)
	if err != nil {
		return out, bad
	}
	var c EnvironmentCacheActivityCursor
	if json.Unmarshal(raw, &c) != nil || c.AccountID != account || c.AppID != app || c.ProjectID != project || c.EnvironmentID != env || c.Sequence < 0 || c.Sequence > RouteRequirementsMaxRevision {
		return out, bad
	}
	return c, nil
}
func EncodeEnvironmentCacheActivityCursor(c EnvironmentCacheActivityCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (c *Client) GetEnvironmentCacheScheduleActivity(ctx context.Context, slug, env, after string, limit int) (EnvironmentCacheScheduleActivity, error) {
	var out EnvironmentCacheScheduleActivity
	n, err := NormalizeEnvironmentCacheActivityPage(after, limit)
	if err != nil {
		return out, err
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/cache/schedules/activity?environment=" + url.QueryEscape(env)
	err = c.do(ctx, "GET", path+"&after="+url.QueryEscape(after)+"&limit="+strconv.Itoa(n), nil, &out)
	return out, err
}

// EnvironmentCacheActivityRecovery describes feed bounds and expired-prefix recovery. Capture the
// restart cursor BEFORE listing a current schedule baseline, then consume events
// after it; baseline/event overlap is expected and must be reconciled by identity.
type EnvironmentCacheActivityRecovery struct {
	AppID            string `json:"app_id"`
	ProjectID        string `json:"project_id"`
	EnvironmentID    string `json:"environment_id"`
	Scope            string `json:"scope"`
	OldestCursor     string `json:"oldest_cursor"`
	RestartCursor    string `json:"restart_cursor"`
	RetentionSeconds int    `json:"retention_seconds"`
}

// GetEnvironmentCacheScheduleActivityCheckpoint captures a scoped feed head in
// bounded work without consuming events or scanning the retained backlog.
func (c *Client) GetEnvironmentCacheScheduleActivityCheckpoint(ctx context.Context, slug, env string) (EnvironmentCacheActivityRecovery, error) {
	var out EnvironmentCacheActivityRecovery
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/cache/schedules/activity/checkpoint?environment="+url.QueryEscape(env), nil, &out)
	return out, err
}
