package api

import (
	"context"
	"net/url"
	"time"
)

type AutomationFailurePolicy struct {
	Version          int64 `json:"version"`
	Enabled          bool  `json:"enabled"`
	FailureThreshold int   `json:"failure_threshold"`
	MinCompletedRuns int   `json:"min_completed_runs"`
	WindowSeconds    int   `json:"window_seconds"`
}
type SetAutomationFailurePolicyRequest struct {
	ExpectedVersion  int64 `json:"expected_version"`
	Enabled          *bool `json:"enabled"`
	FailureThreshold int   `json:"failure_threshold"`
	MinCompletedRuns int   `json:"min_completed_runs"`
	WindowSeconds    int   `json:"window_seconds"`
}
type ResumeAutomationFailurePauseRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}
type AutomationFailureTransition struct {
	Generation     int64     `json:"generation"`
	State          string    `json:"state"`
	Reason         string    `json:"reason"`
	RecordedAt     time.Time `json:"recorded_at"`
	Failures       int64     `json:"failures"`
	CompletedRuns  int64     `json:"completed_runs"`
	PolicyVersion  int64     `json:"policy_version"`
	ActorAccountID string    `json:"actor_account_id,omitempty"`
}
type AutomationFailurePolicyResponse struct {
	Policy                AutomationFailurePolicy       `json:"policy"`
	Paused                bool                          `json:"paused"`
	Generation            int64                         `json:"generation"`
	MonitoringSince       *time.Time                    `json:"monitoring_since,omitempty"`
	PausedAt              *time.Time                    `json:"paused_at,omitempty"`
	ObservedFailures      int64                         `json:"observed_failures"`
	ObservedCompletedRuns int64                         `json:"observed_completed_runs"`
	PendingRuns           int64                         `json:"pending_runs"`
	RunningRuns           int64                         `json:"running_runs"`
	WaitingRuns           int64                         `json:"waiting_runs"`
	RetainedEvents        int64                         `json:"retained_events"`
	History               []AutomationFailureTransition `json:"history"`
}

func failurePolicyPath(slug, name string) string {
	return automationPath(slug) + "/" + url.PathEscape(name) + "/failure-policy"
}
func (c *Client) GetAutomationFailurePolicy(ctx context.Context, slug, name string) (AutomationFailurePolicyResponse, error) {
	var out AutomationFailurePolicyResponse
	err := c.do(ctx, "GET", failurePolicyPath(slug, name), nil, &out)
	return out, err
}
func (c *Client) SetAutomationFailurePolicy(ctx context.Context, slug, name string, body SetAutomationFailurePolicyRequest) (AutomationFailurePolicyResponse, error) {
	var out AutomationFailurePolicyResponse
	err := c.do(ctx, "PUT", failurePolicyPath(slug, name), body, &out)
	return out, err
}
func (c *Client) ResumeAutomationFailurePause(ctx context.Context, slug, name string, body ResumeAutomationFailurePauseRequest) (AutomationFailurePolicyResponse, error) {
	var out AutomationFailurePolicyResponse
	err := c.do(ctx, "POST", failurePolicyPath(slug, name)+"/resume", body, &out)
	return out, err
}
