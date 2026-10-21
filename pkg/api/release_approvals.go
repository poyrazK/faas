package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Release approvals (ADR-20261020085706).
const (
	ReleaseApproversAdmin     = "admin"
	ReleaseApproversDeveloper = "developer"

	ReleaseApprovalDecisionApprove = "approve"
	ReleaseApprovalDecisionReject  = "reject"

	// Approval states of one release.
	ReleaseApprovalStateNotRequired = "not_required"
	ReleaseApprovalStateWaiting     = "waiting"
	ReleaseApprovalStateApproved    = "approved"
	ReleaseApprovalStateRejected    = "rejected"
	ReleaseApprovalStateExpired     = "expired"
)

// ReleaseApprovalPolicy is an app's approval policy.
type ReleaseApprovalPolicy struct {
	App               string `json:"app"`
	RequiredApprovals int    `json:"required_approvals"`
	AfterStage        int    `json:"after_stage"`
	TimeoutMinutes    int    `json:"timeout_minutes"`
	Approvers         string `json:"approvers"`
	AllowSelfApproval bool   `json:"allow_self_approval"`
	// NotifyChannels names the notification channels told when a release
	// waits, is approved or rejected, or times out.
	NotifyChannels []string `json:"notify_channels"`
	UpdatedBy      string   `json:"updated_by"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

// SetReleaseApprovalPolicyRequest is the body of
// PUT /v1/apps/{slug}/release-approval-policy. Zero values take the defaults:
// after stage 1, a 24-hour timeout and owner or admin approvers.
type SetReleaseApprovalPolicyRequest struct {
	RequiredApprovals int    `json:"required_approvals"`
	AfterStage        int    `json:"after_stage,omitempty"`
	TimeoutMinutes    int    `json:"timeout_minutes,omitempty"`
	Approvers         string `json:"approvers,omitempty"`
	AllowSelfApproval bool   `json:"allow_self_approval,omitempty"`
	// NotifyChannels names Slack or email notification channels.
	NotifyChannels []string `json:"notify_channels,omitempty"`
}

// Normalize fills the defaults and validates the request.
func (r SetReleaseApprovalPolicyRequest) Normalize() (SetReleaseApprovalPolicyRequest, error) {
	if r.RequiredApprovals < 1 || r.RequiredApprovals > ReleaseApprovalsRequiredMax {
		return r, fmt.Errorf("required_approvals must be between 1 and %d", ReleaseApprovalsRequiredMax)
	}
	if r.AfterStage == 0 {
		r.AfterStage = 1
	}
	if r.AfterStage < 1 || r.AfterStage > ReleaseApprovalStageMax {
		return r, fmt.Errorf("after_stage must be between 1 and %d", ReleaseApprovalStageMax)
	}
	if r.TimeoutMinutes == 0 {
		r.TimeoutMinutes = int(ReleaseApprovalTimeoutDefault / time.Minute)
	}
	if lo, hi := int(ReleaseApprovalTimeoutMin/time.Minute), int(ReleaseApprovalTimeoutMax/time.Minute); r.TimeoutMinutes < lo || r.TimeoutMinutes > hi {
		return r, fmt.Errorf("timeout_minutes must be between %d and %d", lo, hi)
	}
	if r.Approvers == "" {
		r.Approvers = ReleaseApproversAdmin
	}
	if r.Approvers != ReleaseApproversAdmin && r.Approvers != ReleaseApproversDeveloper {
		return r, fmt.Errorf("approvers must be %q or %q", ReleaseApproversAdmin, ReleaseApproversDeveloper)
	}
	if len(r.NotifyChannels) > ReleaseApprovalNotifyChannelsMax {
		return r, fmt.Errorf("notify_channels names at most %d channels", ReleaseApprovalNotifyChannelsMax)
	}
	seen := map[string]bool{}
	for _, name := range r.NotifyChannels {
		if seen[name] || strings.TrimSpace(name) == "" {
			return r, fmt.Errorf("notify_channels must name distinct channels")
		}
		seen[name] = true
	}
	return r, nil
}

// DecideReleaseApprovalRequest is the body of
// POST /v1/deployments/{id}/approvals.
type DecideReleaseApprovalRequest struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment,omitempty"`
}

// Validate rejects an unknown decision or an over-long or unprintable
// comment; a rejection needs a comment saying why.
func (r DecideReleaseApprovalRequest) Validate() error {
	if r.Decision != ReleaseApprovalDecisionApprove && r.Decision != ReleaseApprovalDecisionReject {
		return fmt.Errorf("decision must be %q or %q", ReleaseApprovalDecisionApprove, ReleaseApprovalDecisionReject)
	}
	if utf8.RuneCountInString(r.Comment) > ReleaseApprovalCommentMax {
		return fmt.Errorf("comment must be at most %d characters", ReleaseApprovalCommentMax)
	}
	if !utf8.ValidString(r.Comment) || strings.IndexFunc(r.Comment, func(c rune) bool { return c < 0x20 || c == 0x7f }) >= 0 {
		return fmt.Errorf("comment must be printable text on one line")
	}
	if r.Decision == ReleaseApprovalDecisionReject && strings.TrimSpace(r.Comment) == "" {
		return fmt.Errorf("a rejection needs a comment saying why")
	}
	return nil
}

// ReleaseApprovalDecision is one person's decision on a release. Counted is
// false for a decision that does not count toward the policy, such as the
// deployer's own approval when self-approval is off.
type ReleaseApprovalDecision struct {
	Approver  string `json:"approver"`
	Role      string `json:"role"`
	Decision  string `json:"decision"`
	Comment   string `json:"comment,omitempty"`
	Counted   bool   `json:"counted"`
	DecidedAt string `json:"decided_at"`
}

// ReleaseApprovalStatus is one release's approval state.
type ReleaseApprovalStatus struct {
	DeploymentID      string                    `json:"deployment_id"`
	App               string                    `json:"app"`
	State             string                    `json:"state"`
	RequiredApprovals int                       `json:"required_approvals,omitempty"`
	Approvals         int                       `json:"approvals"`
	AfterStage        int                       `json:"after_stage,omitempty"`
	DeployedBy        string                    `json:"deployed_by,omitempty"`
	WaitingSince      string                    `json:"waiting_since,omitempty"`
	ExpiresAt         string                    `json:"expires_at,omitempty"`
	Decisions         []ReleaseApprovalDecision `json:"decisions"`
}

// Remaining is how many more approvals the release needs.
func (s ReleaseApprovalStatus) Remaining() int {
	if s.Approvals >= s.RequiredApprovals {
		return 0
	}
	return s.RequiredApprovals - s.Approvals
}

// ListReleaseApprovalsResponse lists an app's releases waiting for approval.
type ListReleaseApprovalsResponse struct {
	Items []ReleaseApprovalStatus `json:"items"`
}

// ErrReleaseApprovalRequired refuses more traffic for a release that still
// waits for approvals.
func ErrReleaseApprovalRequired(status ReleaseApprovalStatus) *Problem {
	detail := fmt.Sprintf("Release %s of %s needs %d more approval(s) before it can take more traffic.", status.DeploymentID, status.App, status.Remaining())
	if status.State == ReleaseApprovalStateExpired {
		detail = fmt.Sprintf("Release %s of %s waited past its approval timeout and is being aborted.", status.DeploymentID, status.App)
	}
	p := NewProblem(http.StatusConflict, CodeReleaseApprovalRequired, "Release waiting for approval", detail).
		WithHint("An eligible teammate approves with `gregale approvals approve " + status.DeploymentID + "`; the rollout then continues on its next tick.").
		WithDocs(docsBase + "/deploys#release-approvals")
	p.ReleaseApproval = &status
	return p
}

// ErrReleaseApprovalRejected refuses traffic for a rejected release.
func ErrReleaseApprovalRejected(status ReleaseApprovalStatus) *Problem {
	p := NewProblem(http.StatusConflict, CodeReleaseApprovalRejected, "Release rejected",
		fmt.Sprintf("Release %s of %s was rejected and cannot take more traffic.", status.DeploymentID, status.App)).
		WithHint("Deploy a new release; a rejection is final.").
		WithDocs(docsBase + "/deploys#release-approvals")
	p.ReleaseApproval = &status
	return p
}

// ErrReleaseApprovalCanaryRequired refuses a production release that would
// take traffic without a canary while the app requires approvals.
func ErrReleaseApprovalCanaryRequired(app string) *Problem {
	return NewProblem(http.StatusConflict, CodeReleaseApprovalCanaryRequired, "Release approvals need a canary",
		fmt.Sprintf("%s requires approvals for production releases, and this release would take traffic without a canary to wait in.", app)).
		WithHint("Deploy with --rollout safe or fast (or without --rollout immediate and --traffic-percent), or remove the policy with `gregale approvals off`.").
		WithDocs(docsBase + "/deploys#release-approvals")
}

// ErrReleaseApprovalParked refuses traffic by hand for a release that waits
// without traffic while its app requires approvals: it starts through a
// schedule, whose canary can wait (ADR-20261021152201).
func ErrReleaseApprovalParked(app, release string) *Problem {
	return NewProblem(http.StatusConflict, CodeReleaseApprovalCanaryRequired, "Release approvals need a canary",
		fmt.Sprintf("%s requires release approvals, so %s cannot take traffic by hand.", app, release)).
		WithHint(fmt.Sprintf("Start it through a canary that waits for approval: `gregale scheduled-releases set %s --app %s --at now`.", release, app)).
		WithDocs(docsBase + "/deploys#scheduled-releases")
}

// ErrReleaseApprovalSelf refuses the deployer's own approval.
func ErrReleaseApprovalSelf() *Problem {
	return NewProblem(http.StatusForbidden, CodeReleaseApprovalSelf, "Self-approval not allowed",
		"You deployed this release, and the app's approval policy needs someone else to approve it.").
		WithHint("Ask an eligible teammate to run `gregale approvals approve`.")
}

// ErrReleaseApprovalDecided refuses a second decision, or a decision on a
// release that no longer accepts one.
func ErrReleaseApprovalDecided(detail string) *Problem {
	return NewProblem(http.StatusConflict, CodeReleaseApprovalDecided, "Decision not accepted", detail)
}

// ErrReleaseApprovalPolicyNotFound reports an app without a policy.
func ErrReleaseApprovalPolicyNotFound(app string) *Problem {
	return NewProblem(http.StatusNotFound, CodeReleaseApprovalPolicyNotFound, "No approval policy",
		fmt.Sprintf("%s does not require release approvals.", app)).
		WithHint("Require approvals with `gregale approvals require " + app + " --approvals 1`.")
}

// GetReleaseApprovalPolicy returns the app's approval policy.
func (c *Client) GetReleaseApprovalPolicy(ctx context.Context, slug string) (ReleaseApprovalPolicy, error) {
	var out ReleaseApprovalPolicy
	return out, c.do(ctx, http.MethodGet, releaseApprovalPolicyPath(slug), nil, &out)
}

// SetReleaseApprovalPolicy creates or replaces the app's approval policy.
func (c *Client) SetReleaseApprovalPolicy(ctx context.Context, slug string, req SetReleaseApprovalPolicyRequest) (ReleaseApprovalPolicy, error) {
	var out ReleaseApprovalPolicy
	return out, c.do(ctx, http.MethodPut, releaseApprovalPolicyPath(slug), req, &out)
}

// DeleteReleaseApprovalPolicy removes the app's approval policy.
func (c *Client) DeleteReleaseApprovalPolicy(ctx context.Context, slug string) error {
	return c.do(ctx, http.MethodDelete, releaseApprovalPolicyPath(slug), nil, nil)
}

// ListReleaseApprovals lists the app's releases waiting for approval.
func (c *Client) ListReleaseApprovals(ctx context.Context, slug string) (ListReleaseApprovalsResponse, error) {
	var out ListReleaseApprovalsResponse
	return out, c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/release-approvals", nil, &out)
}

// GetReleaseApproval returns one release's approval status.
func (c *Client) GetReleaseApproval(ctx context.Context, deploymentID string) (ReleaseApprovalStatus, error) {
	var out ReleaseApprovalStatus
	return out, c.do(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals", nil, &out)
}

// DecideReleaseApproval approves or rejects a release.
func (c *Client) DecideReleaseApproval(ctx context.Context, deploymentID string, req DecideReleaseApprovalRequest) (ReleaseApprovalStatus, error) {
	var out ReleaseApprovalStatus
	return out, c.do(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(deploymentID)+"/approvals", req, &out)
}

func releaseApprovalPolicyPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/release-approval-policy"
}
