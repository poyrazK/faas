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

func (c *Client) ListWorkflowSchedules(ctx context.Context, slug string) (ListWorkflowSchedulesResponse, error) {
	var response ListWorkflowSchedulesResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/workflows/schedules", nil, &response)
	return response, err
}
