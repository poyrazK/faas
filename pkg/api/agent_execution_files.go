package api

import "time"

// CreateAgentExecutionFileGrantRequest delegates one uploaded file to the
// specified app endpoint, thread and logical call of a published task.
type CreateAgentExecutionFileGrantRequest struct {
	EndpointID string `json:"endpoint_id"`
	ThreadID   string `json:"thread_id"`
	ToolCallID string `json:"tool_call_id"`
	TaskName   string `json:"task_name"`
	Version    int    `json:"version"`
	Path       string `json:"path"`
	StepLabel  string `json:"step_label,omitempty"`
}

type AgentExecutionFileGrant struct {
	GrantToken string    `json:"grant_token"`
	ExpiresAt  time.Time `json:"expires_at"`
}
