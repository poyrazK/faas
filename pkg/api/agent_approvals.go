package api

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Categories of agent actions a person may have to approve
// (ADR-20261104140058).
const (
	AgentApprovalDelete      = "delete"
	AgentApprovalProduction  = "production"
	AgentApprovalCredentials = "credentials"
	AgentApprovalBilling     = "billing"
)

// AgentApprovalBudget is a request that would take an agent past its
// monthly budget (ADR-20261111114059). It is not one of the categories a
// connection chooses: the budget itself decides when it applies.
const AgentApprovalBudget = "budget"

// AgentApprovalCategories lists every category, in display order. An agent
// connection that chose none gets all of them.
var AgentApprovalCategories = []string{AgentApprovalDelete, AgentApprovalProduction, AgentApprovalCredentials, AgentApprovalBilling}

// Agent approval request states.
const (
	AgentApprovalPending  = "pending"
	AgentApprovalApproved = "approved"
	AgentApprovalDenied   = "denied"
	AgentApprovalConsumed = "consumed"
	AgentApprovalExpired  = "expired"
)

// CodeApprovalRequired answers a request an agent key may only make after a
// person approves it.
const CodeApprovalRequired = "approval_required"

// CodeApprovalDenied and CodeApprovalExpired answer an agent that repeats a
// request a person denied, or that expired undecided or unused, within
// AgentApprovalOutcomeWindow (ADR-20261107140000 §6).
const (
	CodeApprovalDenied  = "approval_denied"
	CodeApprovalExpired = "approval_expired"
)

// NormalizeAgentApprovalCategories validates a requested set: "none" alone
// means no category, "all" every one. It returns them deduplicated in
// display order.
func NormalizeAgentApprovalCategories(values []string) ([]string, error) {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		switch {
		case value == "none" && len(values) == 1:
			return []string{}, nil
		case value == "all" && len(values) == 1:
			return slices.Clone(AgentApprovalCategories), nil
		case slices.Contains(AgentApprovalCategories, value):
			seen[value] = true
		default:
			return nil, fmt.Errorf("unknown approval category %q: use %s, all or none", value, strings.Join(AgentApprovalCategories, ", "))
		}
	}
	out := []string{}
	for _, category := range AgentApprovalCategories {
		if seen[category] {
			out = append(out, category)
		}
	}
	return out, nil
}

// AgentApprovalPendingInfo rides on an approval_required problem: the link a
// person opens to decide, and the request it decides.
type AgentApprovalPendingInfo struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	ReviewURL string `json:"review_url"`
	ExpiresAt string `json:"expires_at"`
	// Status is set on approval_denied and approval_expired: denied or
	// expired.
	Status string `json:"status,omitempty"`
	// Note is the person's reason for a denial, when they gave one.
	Note string `json:"note,omitempty"`
}

// AgentApprovalResponse is one approval request of an agent connection.
type AgentApprovalResponse struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	// BodyBound reports whether the approval is tied to the request body;
	// large and multipart bodies are not.
	BodyBound bool   `json:"body_bound"`
	Status    string `json:"status"`
	ReviewURL string `json:"review_url,omitempty"`
	// Agent names the connection: its client name or key label.
	Agent     string `json:"agent,omitempty"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	DecidedAt string `json:"decided_at,omitempty"`
	// Note is the person's reason for a denial, when they gave one.
	Note string `json:"note,omitempty"`
	// Reason is what the agent said the request is for, when it said
	// (ADR-20261110182236). Untrusted agent text.
	Reason string `json:"reason,omitempty"`
}

// AgentApprovalChannelsRequest replaces the notification channels that get
// the approval requests of the account's agents (ADR-20261107140000 §3).
type AgentApprovalChannelsRequest struct {
	Channels []string `json:"channels"`
}

// AgentApprovalChannelsResponse names those channels.
type AgentApprovalChannelsResponse struct {
	Channels []string `json:"channels"`
}

// AgentApprovalsResponse lists approval requests, newest first.
type AgentApprovalsResponse struct {
	Items []AgentApprovalResponse `json:"items"`
}

// CreateKeyWith mints a personal key from a full request, including its
// approval categories.
func (c *Client) CreateKeyWith(ctx context.Context, req CreateKeyRequest) (APIKeyResponse, error) {
	var out APIKeyResponse
	return out, c.do(ctx, "POST", "/v1/keys", req, &out)
}

// CreateOrgAPIKeyWith mints an organization key from a full request.
func (c *Client) CreateOrgAPIKeyWith(ctx context.Context, slug string, req CreateOrgAPIKeyRequest) (APIKeyResponse, error) {
	var out APIKeyResponse
	return out, c.do(ctx, "POST", "/v1/orgs/"+url.PathEscape(slug)+"/keys", req, &out)
}

// GetAgentApproval reads one approval request: an agent its own, a person one
// they may decide.
func (c *Client) GetAgentApproval(ctx context.Context, id string) (AgentApprovalResponse, error) {
	var out AgentApprovalResponse
	err := c.do(ctx, "GET", "/v1/agent-approvals/"+url.PathEscape(id), nil, &out)
	return out, err
}

// ListAgentApprovals lists the caller's requests (an agent) or the pending
// requests the caller may decide (a person).
func (c *Client) ListAgentApprovals(ctx context.Context) (AgentApprovalsResponse, error) {
	var out AgentApprovalsResponse
	err := c.do(ctx, "GET", "/v1/agent-approvals", nil, &out)
	return out, err
}

// GetAgentApprovalChannels names the notification channels that get the
// account's agents' approval requests (ADR-20261107140000 §3).
func (c *Client) GetAgentApprovalChannels(ctx context.Context) (AgentApprovalChannelsResponse, error) {
	var out AgentApprovalChannelsResponse
	err := c.do(ctx, "GET", "/v1/agent-approvals/channels", nil, &out)
	return out, err
}

// SetAgentApprovalChannels replaces those channels; an empty list leaves
// email only.
func (c *Client) SetAgentApprovalChannels(ctx context.Context, channels []string) (AgentApprovalChannelsResponse, error) {
	var out AgentApprovalChannelsResponse
	if channels == nil {
		channels = []string{}
	}
	err := c.do(ctx, "PUT", "/v1/agent-approvals/channels", AgentApprovalChannelsRequest{Channels: channels}, &out)
	return out, err
}
