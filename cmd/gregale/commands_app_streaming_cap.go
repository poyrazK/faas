package main

// `gregale apps streaming-cap <slug>` — operator shell entry point
// for ADR-102 D6's per-app streaming classification probe. Prints
// the plan-level shape returned by the SDK's zero-value
// `GetAppStreamingStatus` call:
//   - status enum (streaming | accept-json-downgrade | flag-disabled
//     | plan-disallows | operator-disabled | upgrade-bypass)
//   - effective_cap_bytes / plan_cap_bytes
//   - flag_enabled / plan_allowed boolean pair
//   - cap_kind ("plan" for this no-argument CLI form; route-aware
//     callers can supply host/path/method through the REST and SDK
//     surfaces — see cmd/apid/handlers_streaming_cap.go)
//
// Reachable two ways (mirrors the routes subcommand pattern):
//   - gregale apps streaming-cap <slug>      (dispatchApps arm in main.go)
//   - gregale app <slug> streaming-cap       (cmdAppDispatch arm in commands5.go)
//
// Both dispatchers thread (slug, args[2:]) so the leaf signature
// matches cmdAppsRoutes / cmdAppSecurity — same (slug, args) shape,
// no route-shape flags in the CLI form, single authed round-trip.
//
// Out of scope: a real-time probe that reflects the operator
// FAAS_GATEWAY_STREAMING env. That lives in the gatewayd process,
// not the apid cache. A customer evaluating "will my next request
// stream?" reads the Streaming-Status response header on a real
// request, not this probe. The probe is the static per-app slice
// only.

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// subStreamingCap is the verb name for `gregale apps streaming-cap ...`
// / `gregale app <slug> streaming-cap ...` (ADR-102 D6). Co-located
// with the cmdAppsStreamingCap leaf, mirroring the subSecurity /
// subRoutes pattern so the dispatcher edit (commands5.go:716 +
// main.go:188) and the verb name live one Edit apart for reviewers.
// Single source of truth: goconst stays quiet, the verb name never
// drifts across the dispatcher arms.
const subStreamingCap = "streaming-cap"

// cmdAppsStreamingCap implements `gregale apps streaming-cap <slug>`
// / `gregale app <slug> streaming-cap`. Reads the streaming-status
// enum + cap snapshot for one app via the SDK-shaped
// GET /v1/apps/{slug}/streaming-cap endpoint (apid-side mirror of
// the gatewayd decideStreaming). The flag set is intentionally
// empty — route-aware host/path/method selection is available to
// SDK callers while this CLI keeps its stable plan-level form; the
// customer can render the response as text or JSON via the package-
// level --json toggle already wired by every other leaf.
//
// Why no flags: keeping this command plan-level avoids making the
// shell's positional syntax ambiguous with the three-part route
// shape. The REST and generated SDKs expose route-aware callers
// without changing this stable command form.
func cmdAppsStreamingCap(slug string, args []string) int {
	_ = args // reserved for a future CLI route-shape form
	if slug == "" {
		PrintUsage(os.Stderr, "usage: gregale apps streaming-cap <slug>", "apps")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppStreamingStatus(context.Background(), slug)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	return renderAppsStreamingCapText(osStdout, resp)
}

// renderAppsStreamingCapText renders the AppStreamingStatus as a
// flat key: value list (SLUG is implicit — the operator invoked
// the leaf with the slug). Order is fixed:
//
//  1. status: <enum>
//  2. effective_cap_bytes: <int>  (cap_writer enforces this)
//  3. plan_cap_bytes:      <int>
//  4. flag_enabled:        <bool>
//  5. plan_allowed:        <bool>
//  6. cap_kind:            <string>
//
// The status line is rendered as a literal string so a pinned-SDK
// migration grep ("status: accept-json-downgrade") survives a
// future enum rename. plan_disallows is shown verbatim with no
// chip — the D5 403 gate means this row is unreachable on a
// properly-validated app, and a literal value lets the operator
// spot a data-tier divergence without confusion.
func renderAppsStreamingCapText(w io.Writer, resp api.AppStreamingStatus) int {
	_, _ = fmt.Fprintf(w, "Streaming classification\n")
	_, _ = fmt.Fprintf(w, "  app_id:                %s\n", resp.AppID)
	_, _ = fmt.Fprintf(w, "  status:                %s\n", resp.Status)
	_, _ = fmt.Fprintf(w, "  effective_cap_bytes:   %d\n", resp.EffectiveCap)
	_, _ = fmt.Fprintf(w, "  plan_cap_bytes:        %d\n", resp.PlanCap)
	_, _ = fmt.Fprintf(w, "  flag_enabled:          %t\n", resp.FlagEnabled)
	_, _ = fmt.Fprintf(w, "  plan_allowed:          %t\n", resp.PlanAllowed)
	if resp.CapKind != "" {
		_, _ = fmt.Fprintf(w, "  cap_kind:              %s\n", resp.CapKind)
	}
	// Operator-actionable hints — surfaced on the text path only,
	// not the JSON path. JSON consumers can grep status themselves.
	switch resp.Status {
	case api.StreamingStatusPlanDisallows:
		_, _ = fmt.Fprintln(w, "  hint: plan tier forbids streaming_enabled=true; CreateApp gate (D5) returns 403 CodePlanStreamingNotAllowed.")
	case api.StreamingStatusFlagDisabled:
		_, _ = fmt.Fprintln(w, "  hint: app has streaming_enabled=false; PATCH streaming_enabled=true to enable, subject to plan tier.")
	case api.StreamingStatusAcceptJSONDowngrade:
		_, _ = fmt.Fprintln(w, "  hint: post-D3 this is informational only; pinned SDKs reading the Streaming-Status response header can detect it.")
	}
	return 0
}
