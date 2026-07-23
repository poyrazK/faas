package api

import "context"

// This file holds SDK methods for endpoints the CLI doesn't (yet)
// wrap but the spec exposes. The list below is the complete diff
// between the OpenAPI routes and the methods declared in
// pkg/api/client.go — every entry here is reachable via
// `pkg/api.Client.<Method>` even though `faas <subcommand>` doesn't
// invoke it today.
//
// Adding a new endpoint to api/openapi.yaml? Add a typed method here
// first; the make sdk-check drift gate (commit 3) catches the case
// where someone ships a route without a method.

// UpdateCron edits a cron's schedule/path/enabled. Currently exposed
// only as the partial CRUD — the CLI's `faas crons` covers
// List/Create/Delete but not Update.
func (c *Client) UpdateCron(ctx context.Context, id string, req UpdateCronRequest) (CronResponse, error) {
	var out CronResponse
	return out, c.do(ctx, "PATCH", "/v1/crons/"+id, req, &out)
}

// UsageSummary returns the account-wide monthly roll-up
// (used_gb_hours, included_gb_hours, overage_gb_hours, overage_cents).
// Distinct from GetUsage which returns per-app rows.
func (c *Client) UsageSummary(ctx context.Context, month string) (UsageSummaryResponse, error) {
	var out UsageSummaryResponse
	path := "/v1/usage/summary"
	if month != "" {
		path += "?month=" + month
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// LogEvent is the parsed shape of one deployment-logs frame. SDK
// callers wrap StreamDeploymentLogs with their own SSE parser and
// json.Unmarshal each frame's data into this type. Defined here so
// the SDK owns the public type instead of leaking the server-side
// shape from cmd/apid/handlers_ext.go.
type LogEvent struct {
	Seq       int64  `json:"seq"`
	Stream    string `json:"stream"` // "stdout" or "stderr"
	Line      string `json:"line"`
	WrittenAt string `json:"written_at"`
}
