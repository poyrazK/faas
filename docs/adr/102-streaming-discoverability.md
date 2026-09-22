# ADR-102 · Streaming discoverability

- **Status:** accepted (2026-08-14, mega-PR)
- **Date:** 2026-08-14
- **Predecessors:** ADR-047 (streaming responses through gatewayd),
  ADR-028 amendment (H2C inner leg, PR #750), ADR-080 (raw-bytes Upgrade
  bridge), ADR-091 D24 §6 (edge-rule `max_body_bytes_streaming` for the
  request-side cap), ADR-046 (per-instance egress metering)
- **Successors:** ADR-102-followup (database invariant shipped by
  `20260922131114370_apps_streaming_plan_check.sql`)

## Context

The M8 launch-readiness audit (2026-08-13) surfaced five customer-facing
gaps in the response streaming path rated ship-blocking:

- **G1.** No discoverability — no spec section titled "Gregale streams
  responses"; no SDK signal of streaming state.
- **G2.** Silent buffered fallback — `streamingFor(h, r, app) bool`
  (pkg/gateway/handler.go:2282) returns `false` on any failure, so a
  customer who pays for `streaming_enabled=true` but sets
  `Accept: application/json`, runs on Free, or hits a request with
  streaming operator-disabled gets buffered behavior with no error.
- **G3.** Undocumented `Accept: application/json` opt-out — the
  per-request override works but isn't in customer docs and isn't
  surfaced on the wire.
- **G5.** Per-endpoint streaming cap — `pkg/api/dto.go:4174` exposes
  `EdgeRuleLimitAction.MaxBodyBytesStreaming` for the **request** side
  (wired via ADR-091 D24 §6). The **response** side at
  `pkg/gateway/handler.go:3227` is not yet extended to honor edge
  rules.
- **G7.** Free + `streaming_enabled=true` falls back silently via
  `streamingFallbackLog` on the buffered path.

The streaming infra (ADR-047 + ADR-028 amendment + ADR-080) is fully
wired end-to-end. The work is **discoverability, default ergonomics,
and removing silent downgrade**.

## Decisions

### D1. Status enum

`pkg/api/limits.go` gains a `StreamingStatus` type with six constants:

| Constant | Wire value | When |
|---|---|---|
| `StreamingStatusStreaming` | `streaming` | All four conjuncts hold; `capWriter` wraps `w`; per-flush metering active. |
| `StreamingStatusAcceptJSONDowngrade` | `accept-json-downgrade` | Request had `Accept: application/json` — **does not down-convert**; status is informational for one release cycle so pinned-SDK customers can self-diagnose. |
| `StreamingStatusFlagDisabled` | `flag-disabled` | `apps.streaming_enabled = false`. |
| `StreamingStatusOperatorDisabled` | `operator-disabled` | `FAAS_GATEWAY_STREAMING` env not set. |
| `StreamingStatusPlanDisallows` | `plan-disallows` | Plan is Free (legacy pre-D5 rows only — D5 closes CreateApp at apid). |
| `StreamingStatusUpgradeBypass` | `upgrade-bypass` | `Connection: Upgrade` — raw-bytes bridge handles this, not the streaming path. |

Six constants, six wire values. The enum stays consistent across the
header stamp, the SDK DTO, and the CLI probe.

### D2. Unconditional `Streaming-Status` response header

Every response carries the canonical header (ADR-102 D2). Hoisted out
of the `if streaming { ... }` block at handler.go:~3899 so buffered
responses also carry it. Precedent: `x-faas-request-id` at
handler.go:3443-3451 (unconditional, set before any code path can
`Write`).

```go
decision := decideStreaming(h, r, app)
w.Header().Set(api.StreamingStatusHeader, string(decision.Status))
if decision.Status == api.StreamingStatusStreaming {
    // existing setupStreamingWriter + observeStart block
}
```

### D3. `Accept: application/json` advisory header

Hard-flip: `isAcceptJSON(r)` no longer downgrades the gate. The
`accept-json-downgrade` enum variant stays in the wire surface for
**one release cycle** so pinned-SDK customers with `Accept:
application/json` defaults can self-diagnose via the status header.

In addition, a `Streaming-Status-Accept-Hint: would-buffer-pre-D3`
advisory header is stamped on the FIRST response after upgrade for
customers whose pinned SDK defaults to `Accept: application/json`.
Detected via the `accept-json-downgrade` flag set on the decision.

After one cycle (~30 days post-merge), the variant is deleted and the
advisory header is dropped. Tracking in this ADR's amendment section
"PR-F: variant retirement."

### D4. Per-endpoint RESPONSE cap

`pkg/gateway/handler.go:2334-2339` extends the existing request-side
cap-selection algorithm to also drive the **response** cap installed
at handler.go:3227 via `capWriter`. The new helper
`effectiveResponseCap(h, app, edgeRule)` returns `(int64, string)`:

```go
if edgeRule != nil && edgeRule.MaxBodyBytesStreaming > 0 {
    return edgeRule.MaxBodyBytesStreaming, "endpoint-rule"
}
return app.Plan.MaxResponseBodyBytes(), "plan"
```

`setupStreamingWriter` at handler.go:3186 takes this cap (passed
through the existing `target` plumbing). The existing `capWriter`
type at handler.go:3251 is unchanged — it just receives a different
cap value.

The `s ≥ b` invariant (`MaxBodyBytesStreaming >= MaxBodyBytes`) from
`pkg/api/dto.go:4188` is enforced at apid validate-time. Runtime
trusts it; no additional invariant check.

### D5. `apid` CreateApp 403 gate

The UpdateApp gate at `cmd/apid/handlers_ext.go:245-252` already
returns **403** `CodePlanStreamingNotAllowed` using
`acct.Plan.StreamingResponseAllowed()`. Mirror this in CreateApp's
`buildApp` at `cmd/apid/handlers.go:200-203`:

```go
if req.StreamingEnabled != nil && *req.StreamingEnabled && !acct.Plan.StreamingResponseAllowed() {
    return state.App{}, api.NewProblem(http.StatusForbidden,
        api.CodePlanStreamingNotAllowed,
        "Streaming responses are not allowed on this plan",
        "Free tier does not support per-app streaming; upgrade to Hobby or higher.")
}
```

Status **403** not 402 — matches UpdateApp so the same error code
returns the same status on POST vs PATCH. Telemetry collapsing on
`code` is uniform.

### D6. SDK probe

`GET /v1/apps/{slug}/streaming-cap` returns the resolved status, cap,
and gate flags pre-flight. Mirrors `GET /v1/apps/{slug}/routes` at
`api/openapi.yaml:1396-1429`. SDK method `GetAppStreamingStatus` in
`pkg/api/client.go` mirrors `GetAppRoutes` at
`pkg/api/client.go:1878-1881`.

### D7. CLI probe

`faas apps streaming-cap <slug>` prints the same data. Mirrors
`cmd/gregale/commands_app_routes.go:45-99`. Three registration sites
(cmd/gregale/main.go:37 + :176-204 + cli_meta.go:165-176).

### D8. CORS expose-headers

`Streaming-Status` and `Streaming-Status-Accept-Hint` are added to
the `Access-Control-Expose-Headers` set in the per-edge-rule CORS
header ops (`pkg/gateway/handler.go:1619-1620` builds the
`ExposeHeaders` join). Browser JS clients see both headers.

### Pre-flight (Stage 0a) — assumption under which this ADR is accepted

This PR was authored under **assumption-zero**: zero Free+`streaming_enabled=true`
rows in production, zero customers with `Accept: application/json` on
streaming apps, zero pinned-SDK customers in the wild.

If any of these queries returns nonzero at merge time, the
responsible clauses (D5 for the first, D3 for the second/third) must
be re-evaluated before the PR merges. The migration CHECK constraint
in ADR-102-followup is the durable guarantee that closes the gap
permanently.

```sql
-- D5 risk
SELECT count(*)
  FROM apps a
  JOIN accounts ac ON ac.id = a.account_id
 WHERE a.streaming_enabled = true AND ac.plan = 'free';
-- D3 risk (raw)
SELECT count(*) FROM access_log l JOIN apps a ON l.app_id = a.id
  WHERE a.streaming_enabled = true AND l.accept LIKE '%application/json%';
-- D3 risk (pinned SDK)
SELECT l.sdk_version, count(*) FROM access_log l
  WHERE l.accept LIKE '%application/json%' GROUP BY l.sdk_version;
```

## Follow-up completed

- **CHECK constraint** (`apps_streaming_enabled_plan_check`) — shipped in
  `20260922131114370_apps_streaming_plan_check.sql` after the telemetry
  window. Because `apps` stores `account_id` rather than a plan snapshot,
  the migration uses a stable helper in the CHECK and a constraint trigger
  to reject paid → Free downgrades while an app is still streaming-enabled.

## Deferred work

- **`accept-json-downgrade` enum variant deletion** — after one
  release cycle (~30 days post-merge). Advisory header drops with it.
- **Per-endpoint response cap validation** at apid — verify the `s ≥
  b` invariant from `pkg/api/dto.go:4188` applies to the response cap
  at validate-time (not just runtime trust). Track in §17 gaps.

## References

- pkg/gateway/handler.go:2282 (streamingFor, replaced by decideStreaming)
- pkg/gateway/handler.go:2334-2339 + :3227 (cap-selection extension)
- pkg/gateway/handler.go:3186 (setupStreamingWriter)
- pkg/gateway/handler.go:3443-3451 (x-faas-request-id precedent)
- pkg/gateway/handler.go:3874-3899 (header stamp location)
- pkg/api/limits.go:1155/1382/1608 (Plan.StreamingResponseAllowed accessor)
- pkg/api/dto.go:4173-4194 (EdgeRuleLimitAction.MaxBodyBytesStreaming)
- pkg/api/errors.go:730 (CodePlanStreamingNotAllowed)
- cmd/apid/handlers.go:168-298 (buildApp — D5 gate added)
- cmd/apid/handlers_ext.go:245-252 (UpdateApp gate — model for D5)
- api/openapi.yaml:1396-1429 (path block to mirror for D6)
- pkg/api/client.go:1878-1881 (GetAppRoutes — model for D6 SDK)
- cmd/gregale/commands_app_routes.go:45-99 (model for D7 CLI)
- pkg/gateway/handler.go:1619-1620 (CORS expose-headers — D8)
