package api

import (
	"context"
	"net/url"
)

type WorkflowScheduleResponse struct {
	WorkflowName     string `json:"workflow_name"`
	DeploymentID     string `json:"deployment_id"`
	Schedule         string `json:"schedule"`
	Timezone         string `json:"timezone"`
	Overlap          string `json:"overlap"`
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
