package api

import (
	"context"
	"net/http"
	"net/url"
)

// AgentPreviewHeader asks apid to price a change without making it
// (ADR-20261111114059, amendment 2026-11-13). Its one value is
// AgentPreviewBudget. apid accepts it only on the routes that price every
// accepted request before writing; elsewhere it answers 400.
const (
	AgentPreviewHeader = "Gregale-Agent-Preview"
	AgentPreviewBudget = "budget"
)

// AgentBudgetPreview is apid's answer to a request sent with
// AgentPreviewHeader: what the change would cost the calling agent, and
// whether a person would be asked first. Nothing is written, no approval is
// opened and nothing is recorded.
type AgentBudgetPreview struct {
	// Preview is always true, so a client cannot mistake the answer for
	// the change's own.
	Preview bool `json:"preview"`
	// Charged reports whether the credential pays from an agent budget.
	// People's credentials are never charged; the other fields are then
	// empty.
	Charged bool `json:"charged"`
	// AddedCents is the committed cost the change adds this month, rounded
	// up: a monthly standing cost, or a prewarm's one-off charge. Zero when
	// the change adds nothing.
	AddedCents int64 `json:"added_cents"`
	// BudgetCents is the monthly budget that applies; absent when the agent
	// has none.
	BudgetCents *int64 `json:"budget_cents,omitempty"`
	// SpentCents is what the agent committed this UTC month before the
	// change.
	SpentCents int64 `json:"spent_cents"`
	// RemainingCents is the budget left before the change; absent when
	// unlimited.
	RemainingCents *int64 `json:"remaining_cents,omitempty"`
	// Month is the UTC month spend counts in, as YYYY-MM.
	Month string `json:"month,omitempty"`
	// NeedsApproval reports that the change, sent for real, would wait for
	// a person's approval; ApprovalCategory names why (budget, production,
	// deletes, …). A preview never opens the approval.
	NeedsApproval    bool   `json:"needs_approval"`
	ApprovalCategory string `json:"approval_category,omitempty"`
}

func previewHeaders() http.Header {
	h := http.Header{}
	h.Set(AgentPreviewHeader, AgentPreviewBudget)
	return h
}

func (c *Client) preview(ctx context.Context, method, path string, body any) (AgentBudgetPreview, error) {
	var out AgentBudgetPreview
	err := c.doWithClientAndHeadersAndIdempotencyKeyPolicy(ctx, c.http, method, path, body, &out, "", previewHeaders(), false)
	return out, err
}

// PreviewUpdateApp prices PATCH /v1/apps/{slug} without making it.
func (c *Client) PreviewUpdateApp(ctx context.Context, slug string, req UpdateAppRequest) (AgentBudgetPreview, error) {
	return c.preview(ctx, http.MethodPatch, "/v1/apps/"+url.PathEscape(slug), req)
}

// PreviewAppScaling prices a scaling change without making it.
func (c *Client) PreviewAppScaling(ctx context.Context, app, scope string, req ApplyAppScalingRequest) (AgentBudgetPreview, error) {
	return c.preview(ctx, http.MethodPost, appScalingPath(app, scope, ""), req)
}

// PreviewCreateApp prices POST /v1/apps without creating the app.
func (c *Client) PreviewCreateApp(ctx context.Context, req CreateAppRequest) (AgentBudgetPreview, error) {
	return c.preview(ctx, http.MethodPost, "/v1/apps", req)
}
