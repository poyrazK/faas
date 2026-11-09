package api

import (
	"context"
	"net/url"
	"time"
)

type StoredWorkflowFailurePolicy struct {
	Version          int64 `json:"version"`
	Enabled          bool  `json:"enabled"`
	FailureThreshold int   `json:"failure_threshold"`
	MinCompletedRuns int   `json:"min_completed_runs"`
	WindowSeconds    int   `json:"window_seconds"`
}
type SetStoredWorkflowFailurePolicyRequest struct {
	ExpectedVersion  int64 `json:"expected_version"`
	Enabled          *bool `json:"enabled"`
	FailureThreshold int   `json:"failure_threshold"`
	MinCompletedRuns int   `json:"min_completed_runs"`
	WindowSeconds    int   `json:"window_seconds"`
}
type ResumeStoredWorkflowFailurePauseRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}
type StoredWorkflowFailureTransition struct {
	Generation     int64     `json:"generation"`
	State          string    `json:"state"`
	Reason         string    `json:"reason"`
	RecordedAt     time.Time `json:"recorded_at"`
	Failures       int64     `json:"failures"`
	CompletedRuns  int64     `json:"completed_runs"`
	PolicyVersion  int64     `json:"policy_version"`
	ActorAccountID string    `json:"actor_account_id,omitempty"`
}
type StoredWorkflowFailurePolicyResponse struct {
	Policy                StoredWorkflowFailurePolicy       `json:"policy"`
	Paused                bool                              `json:"paused"`
	Generation            int64                             `json:"generation"`
	MonitoringSince       *time.Time                        `json:"monitoring_since,omitempty"`
	PausedAt              *time.Time                        `json:"paused_at,omitempty"`
	ObservedFailures      int64                             `json:"observed_failures"`
	ObservedCompletedRuns int64                             `json:"observed_completed_runs"`
	PendingRuns           int64                             `json:"pending_runs"`
	RunningRuns           int64                             `json:"running_runs"`
	WaitingRuns           int64                             `json:"waiting_runs"`
	RetainedEvents        int64                             `json:"retained_events"`
	History               []StoredWorkflowFailureTransition `json:"history"`
}

func failurePolicyPath(slug, name string) string {
	return storedWorkflowPath(slug) + "/" + url.PathEscape(name) + "/failure-policy"
}
func (c *Client) GetStoredWorkflowFailurePolicy(ctx context.Context, slug, name string) (StoredWorkflowFailurePolicyResponse, error) {
	var out StoredWorkflowFailurePolicyResponse
	err := c.do(ctx, "GET", failurePolicyPath(slug, name), nil, &out)
	return out, err
}
func (c *Client) SetStoredWorkflowFailurePolicy(ctx context.Context, slug, name string, body SetStoredWorkflowFailurePolicyRequest) (StoredWorkflowFailurePolicyResponse, error) {
	var out StoredWorkflowFailurePolicyResponse
	err := c.do(ctx, "PUT", failurePolicyPath(slug, name), body, &out)
	return out, err
}
func (c *Client) ResumeStoredWorkflowFailurePause(ctx context.Context, slug, name string, body ResumeStoredWorkflowFailurePauseRequest) (StoredWorkflowFailurePolicyResponse, error) {
	var out StoredWorkflowFailurePolicyResponse
	err := c.do(ctx, "POST", failurePolicyPath(slug, name)+"/resume", body, &out)
	return out, err
}
