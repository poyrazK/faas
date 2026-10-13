package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type EventRecoveryControlRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (r EventRecoveryControlRequest) Validate() error {
	if !utf8.ValidString(r.Reason) || len(r.Reason) > EventRecoveryReasonMaxBytes {
		return fmt.Errorf("reason must be valid UTF-8 and at most %d bytes", EventRecoveryReasonMaxBytes)
	}
	for _, ch := range r.Reason {
		if unicode.IsControl(ch) {
			return fmt.Errorf("reason must not contain control characters")
		}
	}
	return nil
}

type EventRecoveryHistoryEntry struct {
	TargetPosition    *int64    `json:"target_position,omitempty"`
	TargetRunID       string    `json:"target_run_id,omitempty"`
	TargetResumeCount *int      `json:"target_resume_count,omitempty"`
	ID                int64     `json:"id"`
	OccurredAt        time.Time `json:"occurred_at"`
	Action            string    `json:"action"`
	ActorKind         string    `json:"actor_kind"`
	ActorID           string    `json:"actor_id"`
	Reason            string    `json:"reason,omitempty"`
	PreviousState     string    `json:"previous_state"`
	State             string    `json:"state"`
	PreviousRate      int       `json:"previous_rate"`
	Rate              int       `json:"rate"`
}
type EventRecoveryHistory struct {
	JobID     string                      `json:"job_id"`
	Entries   []EventRecoveryHistoryEntry `json:"entries"`
	NextAfter int64                       `json:"next_after,omitempty"`
}

func (c *Client) ListEventRecoveryHistory(ctx context.Context, id string, after int64, limit int) (EventRecoveryHistory, error) {
	var out EventRecoveryHistory
	v := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(id)+"/history?"+v.Encode(), nil, &out)
	return out, err
}
func (c *Client) PauseEventRecoveryWithReason(ctx context.Context, id string, req EventRecoveryControlRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/pause", req, &out)
	return out, err
}
func (c *Client) ResumeEventRecoveryWithReason(ctx context.Context, id string, req EventRecoveryControlRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/resume", req, &out)
	return out, err
}
func (c *Client) CancelEventRecoveryWithReason(ctx context.Context, id string, req EventRecoveryControlRequest) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(id)+"/cancel", req, &out)
	return out, err
}

// WorkflowRecoveryCancellationRequest pins cancellation to an admitted generation.
type WorkflowRecoveryCancellationRequest struct {
	RequestID           string `json:"request_id,omitempty"`
	Position            int64  `json:"position"`
	ExpectedResumeCount *int   `json:"expected_resume_count"`
	Reason              string `json:"reason"`
}

func (r WorkflowRecoveryCancellationRequest) Validate() error {
	if r.RequestID != "" {
		id, err := uuid.Parse(r.RequestID)
		if err != nil || id == uuid.Nil || id.String() != r.RequestID {
			return fmt.Errorf("request_id must be a canonical nonzero UUID")
		}
	}

	if r.Position < 1 || r.Position > AutomationRecoveryBatchMaxRuns || r.ExpectedResumeCount == nil || *r.ExpectedResumeCount < 1 || *r.ExpectedResumeCount > WorkflowRunMaxResumes || len(strings.TrimSpace(r.Reason)) == 0 {
		return fmt.Errorf("provide an admitted position, expected resume count and reason")
	}
	return (EventRecoveryControlRequest{Reason: r.Reason}).Validate()
}

// WorkflowRecoveryCancellationResponse is an advisory preview or committed outcome.
type WorkflowRecoveryCancellationResponse struct {
	JobID       string `json:"job_id"`
	Position    int64  `json:"position"`
	RunID       string `json:"run_id,omitempty"`
	ResumeCount int    `json:"resume_count"`
	Status      string `json:"status,omitempty"`
	Outcome     string `json:"outcome"`
	Preview     bool   `json:"preview"`
}

func (c *Client) PreviewWorkflowRecoveryCancellation(ctx context.Context, job string, req WorkflowRecoveryCancellationRequest) (WorkflowRecoveryCancellationResponse, error) {
	var out WorkflowRecoveryCancellationResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(job)+"/workflow-cancellation-preview", req, &out)
	return out, err
}
func (c *Client) CancelWorkflowRecoveryExecution(ctx context.Context, job string, req WorkflowRecoveryCancellationRequest) (WorkflowRecoveryCancellationResponse, error) {
	var out WorkflowRecoveryCancellationResponse
	if err := req.Validate(); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodPost, "/v1/event-recoveries/"+url.PathEscape(job)+"/workflow-cancel", req, &out)
	return out, err
}

// WorkflowRecoveryActionReceipt retains an applied decision, including refusals.
type WorkflowRecoveryActionReceipt struct {
	Request    WorkflowRecoveryCancellationRequest  `json:"request"`
	Response   WorkflowRecoveryCancellationResponse `json:"response"`
	CapturedAt time.Time                            `json:"captured_at"`
	ActorKind  string                               `json:"actor_kind"`
	ActorID    string                               `json:"actor_id"`
}

func (c *Client) GetWorkflowRecoveryActionReceipt(ctx context.Context, job, request string) (WorkflowRecoveryActionReceipt, error) {
	var out WorkflowRecoveryActionReceipt
	err := c.do(ctx, http.MethodGet, "/v1/event-recoveries/"+url.PathEscape(job)+"/workflow-action-receipts/"+url.PathEscape(request), nil, &out)
	return out, err
}
func (c *Client) GetEventRecoveryByRequest(ctx context.Context, app, request string) (EventRecoveryJob, error) {
	var out EventRecoveryJob
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/event-recoveries/by-request/"+url.PathEscape(request), nil, &out)
	return out, err
}
