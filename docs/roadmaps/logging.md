# Logging roadmap — bringing the platform's user-facing logs to parity

- **Owner:** TBD (likely Move 4 / issue #254 thread owner)
- **Status:** proposed (2026-07-29)
- **Source of truth (in order):** `docs/faas_implementation_spec.md` → this roadmap → `docs/adr/` → `ex44_faas_financial_model.xlsx`

## 1. Current state (one paragraph, what a customer sees today)

The platform ships **three** customer-visible SSE surfaces: `GET /v1/apps/{slug}/logs`
(app logs), `GET /v1/deployments/{id}/logs` (build logs), `GET /v1/events` (account
event bus). Build logs work end-to-end (CLI streams real frames; `deployment_logs`
Postgres table persists them; `failure_class` UX copy lands). Account events work
end-to-end (dashboard badges refresh via SSE). **App logs are wire-complete but
production-broken**: `cmd/apid/main.go` never dials schedd (`withScheddClient` is
absent; the server holds `stubScheddClient{}` from `newServerWithDeps` at
`cmd/apid/server.go:468`), so every `faas logs` invocation today returns
`event: degraded {reason: schedd_unreachable}` and the CLI exits 0. The dashboard
has no logs tab. Retention is ring-only (~10 MiB per instance); `deployment_logs`
has no GC. ADR-043 line 16 left archive-to-S3 as gap G6 — unaddressed.

## 2. Gaps to close

| # | Gap | Customer-visible symptom today | Where it lands |
|---|---|---|---|
| G-L1 | Production schedd dial not wired in `cmd/apid/main.go` | `faas logs <app>` returns `degraded` and exits 0 | This roadmap → PR-1 |
| G-L2 | App log stream lives in apid, not gatewayd | A misconfigured gatewayd could let a customer brute-force apid via a separate per-IP bucket (split buckets ≠ spec §11's "10/min/IP" budget) | PR-1 (Move 4 PR-2) |
| G-L3 | No per-instance filter (`?instance=<uuid>`) on app logs | Customers debugging a hot instance can't isolate its frames from sibling-instance frames | PR-2 |
| G-L4 | Dashboard has no logs tab | Spec promises one (`docs/faas_ux_spec.md:168`); ADR-043 line 6 calls this out | PR-3 |
| G-L5 | No structured-log capture (level + request_id inside the ring) | Customer logs are raw stdout/stderr text only; can't grep by level or trace a single invocation | PR-4 |
| G-L6 | No Postgres spool for app logs | Once an instance is parked, its ring is gone — no way to see logs from a request that ran 2 hours ago on a now-evicted instance | PR-5 |
| G-L7 | `deployment_logs` has no retention sweep | Build logs grow unbounded; disk pressure eventually triggers §17 G1 | PR-6 |
| G-L8 | No cross-app / time-range / regex search | Customers stuck with `since + grep` substring filter; can't query across all apps or use regex | PR-7 (deferred — punt to v2 unless a customer asks) |
| G-L9 | No Alertmanager rule on log volume / `apid_logs_emitted_total{app}` drift | Operator has no signal that "log stream is broken" without reading `journald` | PR-6 (alongside the retention GC) |
| G-L10 | No Loki deployment (spec §12 line 586 / §13 line 609 reserve 256 MB for `loki/promtail agents` but no config ships) | Operator-side, not customer-visible; but spec promises it | Out of v1 scope (spec §16 retains it as an open question) |

## 3. PR slices (in order)

Each slice is sized for the CLAUDE.md "reviewable in ~10 min" rule. A PR is done
when its acceptance criteria pass on `make test` + `make lint` + the relevant
remote `run_in_background` jobs, **not** when the code is written. Architecture
changes name an ADR; quota/limit changes edit `pkg/api/limits.go` + this doc.

### PR-1 — Move 4 PR-2: gatewayd owns app logs (closes G-L1, G-L2)

- **Source branch:** `worktree-issue-254-move4-pr2-gatewayd-handler`
- **Closes:** Move 4 PR-A's missing follow-up (ADR-043 line 10, `docs/STATUS.md:524-530`).
- **Scope:**
  - Add `cmd/gatewayd/app_logs.go::AppLogsHandler` (move `streamAppLogs` + `serveAppLogs`
    body from `cmd/apid/handlers_ext.go:1974-2197`).
  - Lift `startSSE` / `writeAppLogEvent` / `renderStreamError` / `validateLogFilters`
    to `pkg/apislogs/sse.go`.
  - Add `cmd/gatewayd/proxy.go::isApidLogsPath` (hand-rolled matcher, not regexp —
    per-request cost; memory `gatewayd-isapidpath-pr180-gap`).
  - Short-circuit `apidProxy.ServeHTTP` when the request matches.
  - Delete `cmd/apid/handlers_ext.go::streamAppLogs` + `serveAppLogs` + the route
    registration at `cmd/apid/server.go:605` + `cmd/apid/schedd_client.go`.
  - Move the seven whitebox `TestServeAppLogs_*` tests to `cmd/gatewayd/app_logs_test.go`,
    swapping `schedLogFrame` for `scheddgrpc.LogFrame`.
  - Wire `withScheddClient` in `cmd/apid/main.go` for any remaining apid-side use
    (likely none after the move — confirm in PR review).
  - Fix the CLI wording at `cmd/faas/commands2.go:1230`:
    `"schedd StreamAppLogs gRPC pending — production wiring"` →
    `"Log stream degraded: the scheduler is temporarily unavailable"`.
  - Share the `pkg/auth/middleware.Middleware.Limiter` bucket between apid and
    gatewayd for `authLimited` (per-IP single bucket for §11 10/min/IP — split
    buckets would let a customer brute-force apid via gatewayd).
- **Acceptance:**
  - `faas logs <app>` on a running app streams real frames within 200 ms.
  - `event: degraded {reason: schedd_unreachable}` is only emitted when schedd
    actually returns a gRPC error (not on stub `Unimplemented`).
  - Per-IP brute-force test: 11 failed auth attempts against gatewayd logs route
    in 60 s → 11th returns 429; same source against apid's `/v1/apps` route
    returns 429 at the 11th (shared bucket, not per-daemon).
  - `apid control-plane-only` depguard carve-out extended: `cmd/gatewayd` allowed
    imports for `pkg/scheddgrpc`, `pkg/auth/middleware`, `pkg/apislogs`.
  - `make test`, `make lint`, `make sdk-check`, `make spec-check` green.
- **ADRs:** amend ADR-043 (drop "PR-B dials it" sentence; record ADR-046 share-bucket
  precedent); amend ADR-046 (record the gatewayd-side Limiter sharing).
- **Spec edits:** none (this is the originally-promised Move 4 PR-B).

### PR-2 — Per-instance filter on app logs (closes G-L3)

- **Source branch:** `worktree-issue-254-pr2-log-instance-filter`
- **Scope:**
  - `cmd/gatewayd/app_logs.go`: accept `?instance=<uuid>` query parameter, parse
    via `pkg/api.IsValidInstanceID` (new, in `pkg/api/logs.go`), reject malformed
    with `event: error {code: invalid_instance}` (matches `invalid_level` pattern).
  - Forward the filter to `pkg/scheddgrpc.Client.StreamAppLogs` via a new field on
    `scheddgrpc.LogFilter` (additive per ADR-016).
  - `pkg/scheddgrpc/server.go`: fan-out to vmmd's `Logs.Subscribe` with the
    `instance_id` filter; vmmd-side `pkg/vmmdgrpc/logs.go` already has the field.
  - `cmd/faas/commands2.go` + `pkg/api/logs.go`: add `--instance <uuid>` flag to
    `faas logs`; client-side validation.
  - SDKs: additive `LogFilter.Instance` field on Go/Node/Python generated types
    + `make sdk-check` green.
  - OpenAPI: extend `api/openapi.yaml:1345-1385` with the `instance` parameter.
- **Acceptance:**
  - Whitebox test: 3 instances running, `faas logs --instance <id>` returns only
    frames from that instance.
  - Customer running on a Free plan (cap=1) sees all frames (no sibling instance
    exists); the filter is a no-op on plans with cap=1.
  - OpenAPI regen + `make sdk-check` green.
- **ADRs:** none (additive).
- **Spec edits:** Appendix A line 693: extend with `?instance=<uuid>`.

### PR-3 — Dashboard logs tab (closes G-L4)

- **Source branch:** `worktree-issue-254-pr3-dashboard-logs`
- **Scope:**
  - Add `cmd/apid/handlers_dashboard.go::appLogsPage` route `GET /dashboard/apps/{slug}/logs`
    (or sibling tab — pick the cheapest layout that doesn't grow the nav).
  - New `pkg/dashboard/templates/app_logs.html` + `pkg/dashboard/templates/apps_list.html`
    link injection.
  - Consume the same SSE stream via `hx-ext="sse"` (`sse-connect="/v1/apps/{slug}/logs?follow=1"`).
  - Per ADR-011 spirit: server-rendered HTMX, no SPA build chain. Reuse existing
    `pkg/httpsec` CSP nonce injection (`pkg/dashboard/templates/app_logs.html` scripts).
  - Reuse `pkg/auth/middleware` (PR-1's extraction) for the dashboard chain.
  - Add a "Live tail" toggle that switches between one-shot (last N lines via a new
    `?before_seq=N` parameter on app logs — mirrors `deployment_logs` cursor) and
    follow (default).
- **Acceptance:**
  - Customer at `/dashboard/apps/{slug}/logs` sees a live tail within 200 ms of
    a `curl -X POST` to invoke the app.
  - CSP nonce stamp present on every `<script>` / `<style>` (no inline handlers);
    `pkg/httpsec` lint green.
  - Empty-state (parked app) renders the same `event: end` envelope as the CLI.
  - `make spec-check` green (no OpenAPI change here — the dashboard route is internal).
- **ADRs:** none (ADR-011 already authorises this surface).
- **Spec edits:** §17 G3 follow-up row (move from "RESOLVED" with caveat to
  "fully RESOLVED").

### PR-4 — Structured-log capture: level + request_id in the ring (closes G-L5)

- **Source branch:** `worktree-issue-254-pr4-structured-capture`
- **Scope:**
  - `pkg/fcvm/logbuf/ring.go`: additive `level string` + `request_id string` fields
    on the `LogFrame` struct (omitempty, per ADR-016).
  - `guest/init`: parse the structured-envelope prefix the runner emits; if absent,
    fall back to `level="info"` and `request_id=""` (back-compat with existing
    stdout).
  - `guest/runners/{node22,python312}`: emit `<ts> <level> <request_id> <line>`
    per request. Use `node:http` `X-Request-Id` / Python `WSGI environ['HTTP_X_REQUEST_ID']`
    headers if present, else mint a UUID.
  - `pkg/scheddgrpc.LogFrame`: add the two fields (additive).
  - `pkg/api.LogEvent`: add `Level string` + `RequestID string` (additive).
  - `pkg/api.IsValidLogLevel` already exists; thread `request_id` into a new
    `?request_id=<uuid>` filter on the SSE route (mirrors `--instance`).
  - SDK regen across all three languages + `make sdk-check` green.
  - Update `apid_logs_emitted_total{app}` to add a `level` label (cardinality-safe:
    3 values × bounded apps).
- **Acceptance:**
  - Customer running a Node 22 function sees `event: log {"level":"info","request_id":"..."}`
    by default; explicit `console.error` becomes `level: "error"`.
  - `faas logs --level=error` filters at the wire; verified by a unit test that
    mounts a 3-frame ring with mixed levels.
  - `faas logs --request-id=<uuid>` returns only frames from that invocation.
  - Existing customers on raw stdout (no envelope) see `level: "info"`,
    `request_id: ""` — no breaking change.
- **ADRs:** new **ADR-047** — Structured-log capture format (envelope shape,
    fallback, runner contract). Required because runner shim changes ship.
- **Spec edits:** §4.9 (runners) + §12 line 586 (ring buffer now carries
  `{seq, instance, stream, level, request_id, line, written_at}`).

### PR-5 — Postgres spool for app logs (closes G-L6)

- **Source branch:** `worktree-issue-254-pr5-app-logs-spool`
- **Scope:**
  - Migration `00066_app_logs.sql`: `app_logs (account_id, app_id, instance_id,
    seq bigserial, stream, level, request_id, line, written_at)` partitioned by
    `written_at` weekly. Index `(app_id, seq desc)` + `(request_id)`.
  - `pkg/state/store.go::AppendAppLog` + `ListAppLogs(ctx, appID, beforeSeq, limit)`
    (mirrors `deployment_logs` API).
  - `pkg/fcvm/logbuf/ring.go`: writer goroutine that flushes evicted lines to
    `AppendAppLog` before they're lost (atomic eviction → spool handoff).
  - New retention policy: Free = 1 day, Hobby = 3 days, Pro = 7 days, Scale = 14
    days. Encoded in `pkg/api/limits.go` (CLAUDE.md: "never inline a limit").
  - `pkg/state/sweeper.go` (new): background sweeper runs hourly, deletes rows
    older than `plan.retention_app_logs`.
  - `cmd/gatewayd/app_logs.go`: `?since=<rfc3339>` already exists; spool lets it
    serve frames from before any instance was parked (true replay).
  - OpenAPI + SDK regen.
- **Acceptance:**
  - E2E test: invoke app, park instance, `faas logs --since <before-park>` still
    returns the frames (now served from `app_logs` not the dead ring).
  - Retention test: rows for a Free-plan app older than 24 h are swept; Pro-plan
    rows at 7 d boundary are kept.
  - Sweeper is idempotent (the §11 invariant about partial indices — memory
    `schedd-watchdog-tick-pattern`).
  - `pkg/api/limits.go::PlanRetentionAppLogs` populated for all 4 plans.
  - `make migrations-check` (contiguity + apply) green.
- **ADRs:** new **ADR-048** — App-logs spool + retention policy.
- **Spec edits:** §5 schema (add `app_logs` table); §13 RAM ledger (no change —
  spool is in the postgres 1,536 MB slice); §16 line 658 (close the "10 MB ring
  enough?" question by recording the spool + per-plan retention).

### PR-6 — Build-log retention GC + log-stream Alertmanager rules (closes G-L7, G-L9)

- **Source branch:** `worktree-issue-254-pr6-build-log-gc`
- **Scope:**
  - `pkg/state/sweeper.go`: extend to `deployment_logs`. Retention: 30 days for
    all plans (build logs are tiny per-row; retention is operator-policy, not
    customer-tier).
  - `deploy/ansible/roles/faas-prometheus/templates/alerts/faas.log-stream.yml.j2`:
    - `FaasLogStreamDown` — `apid_logs_emitted_total` rate-of-change < 0.01 over
      5 m for any app emitting > 100 frames/hour (no false positives on idle
      apps).
    - `FaasAppLogRetentionExhausted` — per-plan retention sweeper lag > 6 h.
  - `docs/runbooks/FaasLogStreamDown.md` + `FaasAppLogRetentionExhausted.md`.
  - Alertmanager wiring: reuses `faas.rules.yml` per memory `rebase-conflict-adjacent-yaml-inserts`
    (additive — never edit existing rules in the same PR).
- **Acceptance:**
  - E2E: insert 100-day-old `deployment_logs` row, run sweeper, row gone.
  - `promtool check rules deploy/ansible/.../faas.log-stream.yml.j2` green.
  - Runbook tests (`make runbook-test` if it exists; otherwise manual exercise).
- **ADRs:** none (operator-side, ADR-041 already covers tenant-abuse observability).
- **Spec edits:** §12 row for log-stream SLO + §17 G1 follow-up.

### PR-7 — Cross-app / time-range / regex search (closes G-L8) — DEFERRED

- **Source branch:** TBD
- **Why deferred:** No customer has asked yet; spec §16 line 658 prioritises
  retention; the spool (PR-5) unblocks the most common ask ("I can't see logs
  from before the instance was parked"). Revisit if a paying customer files
  an issue.
- **Scope (when it lands):**
  - New `?q=<regex>` parameter on app logs (replaces `--grep` substring).
  - New `GET /v1/apps/{slug}/log-search?since=&until=&level=&request_id=&q=`
    for paginated search.
  - Reuses `app_logs` spool from PR-5; no new storage.
- **ADRs:** new ADR (slot reserved).

## 4. ADR + spec deltas table

| Slot | Title | Status | Source PR | Notes |
|---|---|---|---|---|
| ADR-043 | App logs producer stream | amend | PR-1 | Drop "PR-B dials it"; record gatewayd ownership + shared AuthLimit |
| ADR-046 | pkg/auth extraction | amend | PR-1 | Record gatewayd-side Limiter sharing |
| **ADR-047** | Structured-log capture envelope | new | PR-4 | Envelope format + runner contract + fallback |
| **ADR-048** | App-logs spool + per-plan retention | new | PR-5 | Schema + retention policy + sweeper cadence |
| ADR-049+ | reserved for PR-7 (search) | — | — | Don't claim until PR-7 starts |

Spec edits summary:

- **§4.9 (runners)** — emit `<ts> <level> <request_id> <line>` envelope.
- **§5 schema** — add `app_logs` table (PR-5).
- **§12 line 586** — ring buffer now carries `{seq, instance, stream, level, request_id, line, written_at}`.
- **§13 RAM ledger** — no change (spool lives in postgres 1,536 MB slice).
- **§16 line 658** — close by recording PR-5's spool + retention.
- **§17 G3** — move "apps/logs" from "RESOLVED with caveat" to "fully RESOLVED" after PR-3.
- **§17 G6** — close (archive-to-S3 deferred to v2 per the §16 lean).
- **Appendix A line 691** — fix `GET /v1/deployments/{id}` → `GET /v1/deployments/{id}/logs` (already deployed; spec drift).
- **Appendix A line 693** — add `?instance=<uuid>`, `?request_id=<uuid>`, `?before_seq=<N>` parameters after PR-2/4/5.

## 5. Decisions taken / decisions still open

Decided (this conversation, 2026-07-29):

- PR-1 IS Move 4 PR-2 (apid → gatewayd transfer). The simpler "only fix the dial" path was rejected.
- Retention posture: ring-only v1 + Postgres spool (no S3 in v1).
- Dashboard logs tab: server-rendered HTMX in this repo (no SPA).
- Filter + structured: filter first (PR-2), structured second (PR-4). Not both at once.

Still open (decide before PR-1 lands):

- **Auth scope for the gatewayd logs route.** Today `requireScope(api.ScopesReadSurface)` = `[ScopeAdmin, ScopeAppsRead]`. After Move 4 PR-2, should the dashboard cookie session also work? (Yes — the dashboard chain already covers it; this is a wire-shape check, not a new scope.)
- **`?before_seq` on app logs vs `?since`.** Spec promises `since` (RFC3339); the spool (PR-5) makes `before_seq` cheaper. Pick one canonical form per PR-5.
- **Dashboard tab vs new route.** Spec phrasing is "click through to one app's logs (tail)"; PR-3 should match. A tab vs a separate page is a styling call — defer to PR-3 author.
- **Per-instance filter cardinality.** PR-2's filter is unbounded per-instance; the Prometheus counter still keys on `app` (not instance) per ADR-043 line 20. Confirm during PR-2 review.

## 6. Risks & mitigations

- **Risk: PR-1's ownership transfer breaks `apid control-plane-only` depguard.** Mitigation: amend `.golangci.yml` carve-out for gatewayd before PR-1 lands; test locally with `go tool golangci-lint run` (memory `golangci-lint-v2-4-0-handler-checklist`).
- **Risk: PR-1 splits the per-IP brute-force bucket if `pkg/auth/middleware.Middleware.Limiter` isn't shared.** Mitigation: load-bearing test in PR-1 acceptance; integration test asserts 11th request against either daemon returns 429.
- **Risk: PR-5's spool doubles Postgres write volume.** Mitigation: spool is fire-and-forget from the ring eviction path (no sync write back into the hot path); retention sweep is idempotent and runs hourly off-peak.
- **Risk: PR-4's envelope breaks customers who write raw stdout.** Mitigation: runner shim only emits envelope; `guest/init` falls back to `level=info`, `request_id=""` for raw lines. Verified by back-compat test.
- **Risk: PR-2/4/5 trip the SDK coverage gate (`make sdk-check`).** Mitigation: per-SDK regen in the same PR; follow memory `sdk-coverage-gate-mr-pattern`.
- **Risk: PR-3 dashboard tab grows `pkg/dashboard/templates/*` past the §13 RAM budget for apid.** Mitigation: apid's slice is 256 MB; templates are bytes on disk + small heap; budget is RAM-resident, not on-disk. Confirm in PR-3 review.
- **Risk: PR-6 sweeper interacts with the schedd watchdog (memory `schedd-watchdog-tick-pattern`).** Mitigation: separate goroutine in a new `pkg/state/sweeper.go`; idempotent deletes; test the partial-index migration pattern.

## Cross-references

- Issue #254 (Move 4, tier-1 ship-blocker) — original PR thread for PR-1.
- `docs/adr/043-app-logs-stream.md` — the ADR being amended by PR-1.
- `docs/adr/046-pkg-auth-extraction.md` — supplies `pkg/auth/middleware` for PR-1 + PR-3.
- `docs/adr/011-thin-dashboard.md` — authorises PR-3.
- `docs/adr/041-tenant-abuse-observability.md` — §11 10/min/IP budget provenance for PR-1.
- `docs/adr/016-vmmd-stats-and-metrics.md` — additive wire-shape precedent for PR-2/4/5.
- `docs/STATUS.md:524-531` — current entry to amend once PR-1 lands.
- Memories: `wire-opsmetrics-single-registry`, `cross-renderer-invariant-pattern`,
  `apid-park-wake-not-a-vmmd-call`, `gofmt-repo-wide-gate`, `whitebox-test-file-pattern`,
  `gatewayd-isapidpath-pr180-gap`, `gateway-listener-instance-eviction`,
  `move-4-follow-up-pkg-auth-plan`, `move-4-architectural-decision-gateway-streaming`,
  `gatewayd-isapidpath-pr180-gap`, `move3-sdk-sse-decoder`,
  `move3-cli-ctrl-c`, `move3-htmx-sse-dashboard`,
  `sdk-coverage-gate-mr-pattern`, `golangci-lint-v2-4-0-handler-checklist`,
  `schedd-watchdog-tick-pattern`, `cron-fired-audit-gap-issue-291-followup`,
  `middleware-authlimit-shared-bucket`.