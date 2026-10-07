package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

type WorkflowScheduleResponse struct {
	WorkflowName     string `json:"workflow_name"`
	DeploymentID     string `json:"deployment_id"`
	Schedule         string `json:"schedule"`
	Timezone         string `json:"timezone"`
	Overlap          string `json:"overlap"`
	CatchUp          string `json:"catch_up,omitempty"`
	CatchUpWindow    string `json:"catch_up_window,omitempty"`
	Enabled          bool   `json:"enabled"`
	NextFireAt       string `json:"next_fire_at,omitempty"`
	LastEvaluatedAt  string `json:"last_evaluated_at,omitempty"`
	LastScheduledFor string `json:"last_scheduled_for,omitempty"`
	LastStatus       string `json:"last_status,omitempty"`
	LastRunID        string `json:"last_run_id,omitempty"`
}

type ListWorkflowSchedulesResponse struct {
	RuntimeEnabled    bool                       `json:"runtime_enabled"`
	UnavailableReason string                     `json:"unavailable_reason,omitempty"`
	Schedules         []WorkflowScheduleResponse `json:"schedules"`
}

// TenantWorkflowScheduleResponse is the tenant-visible cadence for one
// published schedule trigger. Version is zero until the tenant customizes it.
type TenantWorkflowScheduleResponse struct {
	WorkflowName       string `json:"workflow_name"`
	DeploymentID       string `json:"deployment_id"`
	Schedule           string `json:"schedule"`
	Timezone           string `json:"timezone"`
	Overlap            string `json:"overlap"`
	CatchUp            string `json:"catch_up,omitempty"`
	CatchUpWindow      string `json:"catch_up_window,omitempty"`
	Enabled            bool   `json:"enabled"`
	TenantConfigurable bool   `json:"tenant_configurable"`
	Customized         bool   `json:"customized"`
	Version            int64  `json:"version"`
}

type ListTenantWorkflowSchedulesResponse struct {
	Schedules []TenantWorkflowScheduleResponse `json:"schedules"`
}

type UpdateTenantWorkflowScheduleRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Schedule        string `json:"schedule"`
	Timezone        string `json:"timezone,omitempty"`
	Overlap         string `json:"overlap,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
}

func (c *Client) ListTenantWorkflowSchedules(ctx context.Context, slug string) (ListTenantWorkflowSchedulesResponse, error) {
	var response ListTenantWorkflowSchedulesResponse
	err := c.do(ctx, "GET", "/v1/platform-tenant-self/apps/"+url.PathEscape(slug)+"/workflows/schedules", nil, &response)
	return response, err
}

func (c *Client) UpdateTenantWorkflowSchedule(ctx context.Context, slug, workflowName string, request UpdateTenantWorkflowScheduleRequest) (TenantWorkflowScheduleResponse, error) {
	var response TenantWorkflowScheduleResponse
	path := "/v1/platform-tenant-self/apps/" + url.PathEscape(slug) + "/workflows/schedules/" + url.PathEscape(workflowName)
	err := c.do(ctx, "PUT", path, request, &response)
	return response, err
}

func (c *Client) ListWorkflowSchedules(ctx context.Context, slug string) (ListWorkflowSchedulesResponse, error) {
	var response ListWorkflowSchedulesResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/workflows/schedules", nil, &response)
	return response, err
}

// WorkflowScheduleOccurrenceResponse is a due schedule's immutable admission
// outcome, retained independently from the workflow run.
type WorkflowScheduleOccurrenceResponse struct {
	ID               string     `json:"id"`
	AppID            string     `json:"app_id"`
	PlatformTenantID string     `json:"platform_tenant_id,omitempty"`
	WorkflowName     string     `json:"workflow_name"`
	DeploymentID     string     `json:"deployment_id"`
	ScheduledFor     time.Time  `json:"scheduled_for"`
	EvaluatedAt      time.Time  `json:"evaluated_at"`
	Status           string     `json:"status"`
	RunID            string     `json:"run_id,omitempty"`
	ReplayRunID      string     `json:"replay_run_id,omitempty"`
	ReplayedAt       *time.Time `json:"replayed_at,omitempty"`
}

type WorkflowScheduleReplayRequest struct {
	OccurrenceIDs []string `json:"occurrence_ids"`
}

type WorkflowScheduleReplayOutcome struct {
	OccurrenceID     string `json:"occurrence_id"`
	PlatformTenantID string `json:"platform_tenant_id,omitempty"`
	WorkflowName     string `json:"workflow_name,omitempty"`
	ScheduledFor     string `json:"scheduled_for,omitempty"`
	Outcome          string `json:"outcome"`
	ReplayRunID      string `json:"replay_run_id,omitempty"`
}

type WorkflowScheduleReplayResponse struct {
	Outcomes []WorkflowScheduleReplayOutcome `json:"outcomes"`
}

type ListWorkflowScheduleOccurrencesResponse struct {
	Occurrences []WorkflowScheduleOccurrenceResponse `json:"occurrences"`
	NextCursor  string                               `json:"next_cursor,omitempty"`
}

type ListWorkflowScheduleOccurrencesOptions struct {
	PlatformTenantID string
	Cursor           string
	Limit            int
}

func (c *Client) ListWorkflowScheduleOccurrences(ctx context.Context, slug string, options ListWorkflowScheduleOccurrencesOptions) (ListWorkflowScheduleOccurrencesResponse, error) {
	query := url.Values{}
	if options.PlatformTenantID != "" {
		query.Set("platform_tenant_id", options.PlatformTenantID)
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/workflows/schedules/occurrences"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var response ListWorkflowScheduleOccurrencesResponse
	err := c.do(ctx, "GET", path, nil, &response)
	return response, err
}

func (c *Client) PreviewWorkflowScheduleReplays(ctx context.Context, slug string, request WorkflowScheduleReplayRequest) (WorkflowScheduleReplayResponse, error) {
	var response WorkflowScheduleReplayResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/workflows/schedules/occurrences:replay-preview"
	err := c.do(ctx, "POST", path, request, &response)
	return response, err
}

func (c *Client) ReplayWorkflowScheduleOccurrences(ctx context.Context, slug string, request WorkflowScheduleReplayRequest) (WorkflowScheduleReplayResponse, error) {
	var response WorkflowScheduleReplayResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/workflows/schedules/occurrences:replay"
	err := c.do(ctx, "POST", path, request, &response)
	return response, err
}
