package api

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// CreateEnvironmentCacheScheduleRequest delegates one immutable action to apid.
// The schedule ID is also the action's idempotency key.
type CreateEnvironmentCacheScheduleRequest struct {
	ScheduleID    string                              `json:"schedule_id"`
	AppID         string                              `json:"app_id"`
	ProjectID     string                              `json:"project_id"`
	EnvironmentID string                              `json:"environment_id"`
	ExecuteAt     time.Time                           `json:"execute_at"`
	Policy        *SetEnvironmentCachePolicyRequest   `json:"policy,omitempty"`
	Purge         *CreateEnvironmentCachePurgeRequest `json:"purge,omitempty"`
}
type EnvironmentCacheSchedule struct {
	CreateEnvironmentCacheScheduleRequest
	ReplacesScheduleID   string                        `json:"replaces_schedule_id,omitempty"`
	ReplacedByScheduleID string                        `json:"replaced_by_schedule_id,omitempty"`
	Scope                string                        `json:"scope"`
	Status               string                        `json:"status"`
	CreatedAt            time.Time                     `json:"created_at"`
	FinishedAt           *time.Time                    `json:"finished_at,omitempty"`
	ErrorCode            string                        `json:"error_code,omitempty"`
	PolicyResult         *EnvironmentCachePolicy       `json:"policy_result,omitempty"`
	PurgeResult          *EnvironmentCachePurgeReceipt `json:"purge_result,omitempty"`
}

func ValidateEnvironmentCacheSchedule(req CreateEnvironmentCacheScheduleRequest) error {
	for _, id := range []string{req.ScheduleID, req.AppID, req.ProjectID, req.EnvironmentID} {
		if _, err := uuid.Parse(id); err != nil {
			return errors.New("schedule and target UUIDs are required")
		}
	}
	if req.ExecuteAt.IsZero() || (req.Policy == nil) == (req.Purge == nil) {
		return errors.New("execution time and exactly one policy or purge action are required")
	}
	if p := req.Policy; p != nil {
		if ValidateEnvironmentCachePolicyRequest(*p) != nil || p.CopyFrom != nil || p.RestoreRevision != nil || p.AppID != req.AppID || p.ProjectID != req.ProjectID || p.EnvironmentID != req.EnvironmentID || p.RequestID != req.ScheduleID {
			return errors.New("schedule requires an exact replacement policy with bound identities and revision")
		}
	}
	if p := req.Purge; p != nil {
		if ValidateEnvironmentCachePurgeRequest(*p) != nil || p.AppID != req.AppID || p.ProjectID != req.ProjectID || p.EnvironmentID != req.EnvironmentID || p.RequestID != req.ScheduleID {
			return errors.New("schedule purge identities must match")
		}
	}
	return nil
}
func cacheSchedulePath(slug, env, id string) string {
	p := "/v1/apps/" + url.PathEscape(slug) + "/cache/schedules"
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p + "?environment=" + url.QueryEscape(env)
}
func (c *Client) CreateEnvironmentCacheSchedule(ctx context.Context, slug, env string, req CreateEnvironmentCacheScheduleRequest) (EnvironmentCacheSchedule, error) {
	var out EnvironmentCacheSchedule
	err := c.doWithIdempotencyKey(ctx, "POST", cacheSchedulePath(slug, env, ""), req, &out, req.ScheduleID)
	return out, err
}
func (c *Client) GetEnvironmentCacheSchedule(ctx context.Context, slug, env, id string) (EnvironmentCacheSchedule, error) {
	var out EnvironmentCacheSchedule
	err := c.do(ctx, "GET", cacheSchedulePath(slug, env, id), nil, &out)
	return out, err
}
func (c *Client) CancelEnvironmentCacheSchedule(ctx context.Context, slug, env, id string) (EnvironmentCacheSchedule, error) {
	var out EnvironmentCacheSchedule
	err := c.do(ctx, "DELETE", cacheSchedulePath(slug, env, id), nil, &out)
	return out, err
}
