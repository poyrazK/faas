package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// HostedTaskRehearsalPrepare identifies one currently passing native binding observation.
type HostedTaskRehearsalPrepare struct {
	IntentSHA256  string `json:"intent_sha256"`
	ReviewSHA256  string `json:"review_sha256"`
	BindingSHA256 string `json:"binding_sha256"`
}

// HostedTaskRehearsalDecision is a separate owner decision against the exact plan.
type HostedTaskRehearsalDecision struct {
	PlanSHA256 string `json:"plan_sha256"`
	Decision   string `json:"decision"`
}

// HostedTaskRehearsalPlan is persisted metadata for one disposable target.
// TargetID reserves an identity for future provisioning. No resources are
// provisioned when a plan is created, approved or claimed.
type HostedTaskRehearsalPlan struct {
	HostedTaskRehearsalPrepare
	Version          int        `json:"version"`
	AccountID        string     `json:"account_id"`
	IntentID         string     `json:"intent_id"`
	TargetID         string     `json:"target_id"`
	QueueNamespace   string     `json:"queue_namespace"`
	PlanSHA256       string     `json:"plan_sha256"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	BindingCheckedAt time.Time  `json:"binding_checked_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
	ClaimedAt        *time.Time `json:"claimed_at,omitempty"`
	NoTraffic        bool       `json:"no_traffic"`
}

func (c *Client) PrepareHostedTaskRehearsal(ctx context.Context, id string, in HostedTaskRehearsalPrepare) (HostedTaskRehearsalPlan, error) {
	var out HostedTaskRehearsalPlan
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, hostedTaskRehearsalPath(id), in, &out)
	return out, err
}

func (c *Client) GetHostedTaskRehearsal(ctx context.Context, id string) (HostedTaskRehearsalPlan, error) {
	var out HostedTaskRehearsalPlan
	err := c.do(ctx, http.MethodGet, hostedTaskRehearsalPath(id), nil, &out)
	return out, err
}

func (c *Client) DecideHostedTaskRehearsal(ctx context.Context, id string, in HostedTaskRehearsalDecision) (HostedTaskRehearsalPlan, error) {
	var out HostedTaskRehearsalPlan
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, hostedTaskRehearsalPath(id)+"/decision", in, &out)
	return out, err
}

func hostedTaskRehearsalPath(id string) string {
	return "/v1/task-tool-source-intents/" + url.PathEscape(id) + "/rehearsal"
}
