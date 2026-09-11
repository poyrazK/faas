package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// LogFilter narrows the per-instance log stream by substring (Grep),
// timestamp lower-bound (Since, RFC3339), and severity floor (Level,
// one of info|warn|error). Issue #309 closes the tier-2 DX gap that
// `faas logs <app>` had no flag-based filtering — these fields are
// optional; the zero value passes through every line.
//
// Since issue #517 / PR-B the wire contract is enforced end-to-end:
// gatewayd-internal forwards Since to schedd, schedd forwards to vmmd, and
// vmmd applies the bound against the per-instance ring buffer at
// attach time. A cursor below the ring's lowest retained seq emits
// an `event: gap` frame (decoded into LogGapEvent on the SDK side)
// rather than silently replaying from a stale position.
type LogFilter struct {
	// Grep is a literal substring match applied to each log
	// line (case-insensitive). Empty = no grep filter. The match
	// is anchored on occurrence: `--grep=foo.bar` matches the
	// literal characters `foo.bar`, NOT `fooXbar`. This is
	// both the SDK contract (issue #309 / tier-2 DX) AND the
	// implementation in pkg/scheddgrpc.LogFilter — substring
	// semantics were chosen over Go regexp to (a) match
	// customer mental models and (b) close the regex-DoS
	// surface the peer review of PR #728 flagged.
	//
	// Embedded newlines and patterns longer than
	// apislogs.MaxGrepPatternBytes are rejected by the gateway
	// validator (apislogs.ValidateLogFilters).
	Grep string
	// Since is an RFC3339 timestamp lower-bound on the line timestamp.
	Since string
	// Level is a severity floor on the structured `level` field
	// (info, warn, error). Empty = no level filter. The CLI and the
	// apid handler both call IsValidLogLevel before forwarding so an
	// invalid value never reaches the wire.
	//
	// Customer-facing semantics (issue #309 / tier-2 DX):
	//   --level=error passes only error lines
	//   --level=warn   passes warn AND error lines
	//   --level=info   passes info, warn, AND error lines
	//
	// The matcher is a heuristic — it recognises common line
	// shapes (`[ERROR]`, `level=error`, JSON `{"level":"error"}`,
	// `{"severity":"error"}`) and drops anything that doesn't
	// match a recognised marker. A line emitted as bare stdout
	// (e.g. `console.log("request handled")` with no level
	// prefix) is treated as "below the floor" and DROPPED under
	// any non-empty Level value. Customers needing strict
	// filtering of bare stdout should use --grep with an
	// explicit regex instead. The drop counter
	// apid_logs_dropped_total{reason="filter_level"} ticks up
	// on each such drop so the dashboard surfaces the rate.
	Level string
}

// validLogLevels is the canonical set the CLI (cmd/faas/commands2.go)
// and the apid handler (cmd/apid/handlers_ext.go::streamAppLogs) share
// — keep this single source of truth or the two sides will drift.
var validLogLevels = [...]string{"info", "warn", "error"}

// IsValidLogLevel reports whether s is one of the recognised log
// severity values (info, warn, error). Issue #309: used by both the
// CLI to short-circuit before opening the HTTP call and by the apid
// handler to emit an `event: error` SSE frame on a bad value — having
// both call sites agree on the enum is the whole point.
func IsValidLogLevel(s string) bool {
	for _, v := range validLogLevels {
		if v == s {
			return true
		}
	}
	return false
}

// StreamAppLogs opens the GET /v1/apps/{slug}/logs stream and returns
// its raw response body. The response is text/event-stream; callers
// parse frames themselves with bufio.Scanner.
//
// follow=true keeps the stream open after the initial page (server
// keeps writing); follow=false closes it once the initial page has
// been emitted. The returned io.ReadCloser must be Closed by the
// caller — the API keeps the stream open server-side until either
// EOF or context cancellation.
//
// deploymentID filters to a specific deployment (matches the
// `?deployment=` query param the CLI's `faas logs --deployment` uses);
// pass "" to receive all instances' frames. Issue #517 / PR-B:
// the deployment filter is now enforced server-side; Free plan
// customers get a 400 plan_deployment_filter_not_allowed rejection
// before the stream opens.
//
// opts narrows the per-line output by Grep / Since / Level; pass the
// zero LogFilter for an unfiltered stream. Issue #309.
//
// Non-2xx responses are decoded as *APIError (same convention as the
// JSON methods) and the body is closed internally; the caller only
// ever sees a successful body or an error.
func (c *Client) StreamAppLogs(ctx context.Context, slug, deploymentID string, follow bool, opts LogFilter) (io.ReadCloser, error) {
	q := url.Values{}
	if follow {
		q.Set("follow", "1")
	} else {
		q.Set("follow", "0")
	}
	if deploymentID != "" {
		q.Set("deployment", deploymentID)
	}
	if opts.Grep != "" {
		q.Set("grep", opts.Grep)
	}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Level != "" {
		q.Set("level", opts.Level)
	}
	return c.stream(ctx, "/v1/apps/"+url.PathEscape(slug)+"/logs?"+q.Encode())
}

// StreamDeploymentLogs opens GET /v1/deployments/{id}/logs. beforeSeq
// narrows the initial page to rows whose seq is strictly less than
// the cursor (server-side default is 0 = the most recent page); pass
// nil for "open from the live tail". limit caps the initial page
// (server-side default 50, max 500); pass 0 for the default.
//
// follow=true keeps the stream open after the initial page; the server
// emits periodic "event: log" frames as new build/deploy rows land.
// The server sends a single "event: end data: {}" before closing the
// stream when the deployment reaches a terminal status (or after a
// 10-minute backstop).
func (c *Client) StreamDeploymentLogs(ctx context.Context, id string, beforeSeq *int64, limit int, follow bool) (io.ReadCloser, error) {
	path := fmt.Sprintf("/v1/deployments/%s/logs?", id)
	q := ""
	if beforeSeq != nil {
		q += "&before_seq=" + fmt.Sprintf("%d", *beforeSeq)
	}
	if limit > 0 {
		q += "&limit=" + fmt.Sprintf("%d", limit)
	}
	if follow {
		q += "&follow=1"
	} else {
		q += "&follow=0"
	}
	if len(q) > 0 {
		// path already has "?"; strip leading "&"
		path += q[1:]
	}
	return c.stream(ctx, path)
}

// StreamEvents opens GET /v1/events — the multi-channel push surface
// apid forwards to dashboard SSE clients and the CLI's `faas tail`.
//
// The route returns text/event-stream with frames carrying one of the
// dashboard topic names (`app_changed`, `deployment_changed`,
// `instance_changed`, `cron_fired`, `quota_warning`,
// `billing_past_due`, `invocation_done`); the caller filters and
// decodes the JSON payload itself. The server forwards frames to the
// caller as soon as apid's pg_notify fan-in republishes them, so
// end-to-end latency from cron-fires / queue-receives / async invoke
// terminations is dominated by Postgres NOTIFY round-trip (~ms).
//
// Authentication: the route goes through the dashboard auth chain, so
// a Bearer token issued to a customer account only sees frames whose
// account_id matches the caller's. The CLI's `faas tail` is therefore
// safe to ship to the customer — there is no escalation path where
// one customer can tail another customer's invocations.
//
// Caller contract mirrors StreamAppLogs: the returned io.ReadCloser
// MUST be Closed by the caller; the server keeps the SSE connection
// open until EOF or context cancel. The CLI uses
// signal.NotifyContext(os.Interrupt) so a Ctrl-C drops the connection
// inside ~50 ms instead of waiting for the apid-side heartbeat
// timeout.
//
// Move 3 / M7.5 prep — this is the SDK seam for `faas tail`. The
// dashboard uses the same route via the browser EventSource; nothing
// in this slice changes the dashboard's auth path.
func (c *Client) StreamEvents(ctx context.Context) (io.ReadCloser, error) {
	return c.stream(ctx, "/v1/events")
}

// stream is the shared HTTP-execution backbone for both SSE helpers.
// It keeps the request body close discipline in one place so the
// public API can stay "return io.ReadCloser, error" without dragging
// response.closer semantics into every helper.
func (c *Client) stream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.sseClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the API: %w", err)
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, apiErrorFromResponse(resp, data)
	}
	return resp.Body, nil
}
