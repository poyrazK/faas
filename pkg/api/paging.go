package api

import (
	"context"
	"net/http"
	"strconv"
)

// ListDeploymentsAll walks the next_before cursor on
// GET /v1/deployments until the server returns an empty cursor,
// returning every deployment in created_at DESC order. Useful for
// dashboards that want to render "every deploy ever" without forcing
// the customer to wire a loop.
//
// The server caps each page at 200 rows (handled by ListDeployments);
// this method requests max page size when walking.
//
// Cancelling ctx stops the walk at the next page boundary — the
// current page's rows are returned up to the cancellation point.
func (c *Client) ListDeploymentsAll(ctx context.Context) ([]DeploymentResponse, error) {
	var out []DeploymentResponse
	cursor := ""
	for {
		page, err := c.ListDeployments(ctx, cursor, 200)
		if err != nil {
			return out, err
		}
		out = append(out, page.Items...)
		if page.NextBefore == "" {
			return out, nil
		}
		cursor = page.NextBefore
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
}

// GetBuildsAll walks the next_before cursor on GET /v1/builds
// until the server returns an empty cursor, returning every
// build the account owns in started_at DESC NULLS LAST order
// (DEPLOY-PROV-6 follow-up / ADR-091, issue #741 close-out).
// Mirrors ListDeploymentsAll above.
//
// The server caps each page at 200 rows (handled by GetBuilds);
// this method requests max page size when walking. The
// `app`, `status`, and empty-cursor termination conditions all
// propagate through GetBuilds — callers that pass `app` /
// `status` only see the filtered slice walked to completion.
//
// The cursor is the opaque tuple `<started_at>|<id_hex>` from
// the server (post-review fix for the original single-column
// cursor that lost queued-build tails + sub-second rows past
// page 1). The helper threads it verbatim — no parsing here.
// See ADR-091 §3 + cmd/apid/handlers_ext.go::parseBuildCursor.
//
// Cancelling ctx stops the walk at the next page boundary — the
// current page's rows are returned up to the cancellation point.
func (c *Client) GetBuildsAll(ctx context.Context, app, status string) ([]BuildResponse, error) {
	var out []BuildResponse
	cursor := ""
	for {
		page, err := c.GetBuilds(ctx, app, status, cursor, 200)
		if err != nil {
			return out, err
		}
		out = append(out, page.Items...)
		if page.NextBefore == "" {
			return out, nil
		}
		cursor = page.NextBefore
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
}

// ParseLimit parses a ?limit= query value with a strict 400 contract
// (issue #393 — matches /v1/invoices' parseInvoiceListParams shape).
// Returns:
//
//   - (nil, defaultN) when raw is "" — caller passes the default.
//   - (nil, n) when raw parses to an integer in [1, maxN].
//   - (*Problem, 0) when raw is malformed, < 1, or > maxN. The
//     Problem is RFC 7807-shaped, carries the limit + observed value
//     via WithLimit, and pins the docs URL via WithDocs so a customer
//     hitting the cap from a script gets an actionable message.
//   - (nil, n) when raw parses to a number inside the allowed range.
//
// label is the URL-friendly noun used in the WithDocs fragment
// (e.g. "instances", "secrets"). Must be lowercase plural so the
// docs URL stays stable across endpoints.
//
// Call site:
//
//	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 25, 100, "instances")
//	if prob != nil { api.WriteProblem(w, prob); return }
//
// The matching free function (vs. a method) keeps the import cycle
// out of cmd/apid: pkg/api does not import cmd/apid, but cmd/apid
// already imports pkg/api, so the call direction is one-way.
func ParseLimit(raw string, defaultN, maxN int, label string) (*Problem, int) {
	if raw == "" {
		return nil, defaultN
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxN {
		observed := int64(0)
		if err == nil {
			observed = int64(n)
		}
		return NewProblem(http.StatusBadRequest, CodeValidation,
			"Bad limit", "expected 1.."+strconv.Itoa(maxN)).
			WithLimit(int64(maxN), observed).
			WithDocs(docsBase + "/api#pagination"), 0
	}
	return nil, n
}
