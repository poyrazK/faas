# ADR-117 · Deploy stage progress (typed `event: stage` SSE frame + CLI ticker)

- **Status:** **Accepted** (PR #985 merged 2026-08-19; post-stream
  `gregale deploys show <id>` companion shipped in the follow-on PR
  on branch `worktree-feat-deploys-show-summary`)
- **Date:** 2026-08-18 (initial) · 2026-08-19 (post-stream addition)
- **Decision:** The `/v1/deployments/{id}/logs` SSE stream
  publishes a typed `event: stage` frame for each named pipeline
  stage the customer's deploy passes through. The closed 6-stage
  vocabulary is owned by `pkg/state.StageName` and is the canonical
  surface for any future ticker UI (CLI today, dashboard tomorrow).
  The CLI renders the stream as a live ticker (TTY-gated ANSI
  cursor-up redraw) with a static fallback for pipes / `--json` /
  `NO_COLOR`. The deploy UX moves from "single spinner" to
  "6-row named progress block with per-stage elapsed time" — the
  same affordance Render's deploy log exposes. The follow-on
  companion surface (`gregale deploys show <id>`) re-reads the
  persisted `stage_state` jsonb via
  `GET /v1/deployments/{id}/stages` and renders the same 6-row
  block statically for post-mortem / support hand-off — the
  closed vocabulary is shared by both surfaces, no parallel DTO.

## Context

`gregale deploy` today prints one `→ build queued …` line, then
streams raw builderd stdout via `event: log`, and ends with
`✓ Deployed. …`. The deployment pipeline already has clean
internal stage boundaries (`pending → building → imaging →
snapshotting → live | failed | superseded` on `deployments.status`)
but the wire surface exposes them only as one coarse field — the
CLI cannot render per-stage elapsed timing. Customers want the
same confidence-building narrative that Render's deploy log gives:
a list of named stages with the time each one took, prominent in
the deploy output.

This is the same shape customers have come to expect from every
PaaS deploy (Render, Fly, Heroku, Vercel). Without it Gregale's
deploy UX looks unfinished next to those platforms.

## Decision

### Wire vocabulary

A single typed SSE frame carries the per-stage progress:

```
event: stage
data: {"name":"<StageName>","started_at":"<RFC3339Nano>","duration_ms":<int64>,"status":"in_progress"|"completed"|"failed"[,"reason":"<string>"]}
```

- `name` ∈ closed set of 6 stages (see A1 below).
- `status:"in_progress"` emitted once per stage on entry.
- `status:"completed"` emitted once per stage on exit (with
  `duration_ms` measured server-side).
- `status:"failed"` emitted once for the active stage on
  `DeployFailed`, carrying the optional `reason` from
  `deployments.failure_reason`.

The frame is additive — every existing SSE consumer that doesn't
decode `event: stage` is unaffected. The `pkg/api/sse.go` decoder
already preserves `event:` names verbatim, so SDK consumers don't
need a regen.

A1 — closed 6-stage vocabulary:

| StageName           | Human label          | Server-side transition sites                                       |
|---------------------|----------------------|--------------------------------------------------------------------|
| `source_download`   | Source downloaded    | imaged `handleDeploySourceChanged` Pending→Building               |
| `dependency_restore`| Dependencies restored| imaged `buildImageLayer` / `buildFunctionLayer` Building→Imaging  |
| `image_build`       | Image built          | imaged `handleDeploySourceChanged` Imaging→Snapshotting           |
| `security_scan`     | Security scan        | imaged `buildImageLayer` scan-completion stamp                    |
| `snapshot_prepare`  | Snapshot prepared    | imaged `handleSnapshotBoot` Imaging→Snapshotting                  |
| `readiness`         | Readiness passed     | imaged `MarkDeploymentLive` snapshot_prepare→readiness close       |

The customer-visible set omits builderd-internal micro-states
(`imaging` / `snapshotting` are one user-visible "image build"
step) and omits the terminal `Deployment live` row since
`✓ Deployed. …` already conveys it.

### Schema

`deployments.stage_state jsonb NOT NULL DEFAULT
'{"current":"source_download","current_started_at":null,"history":[]}'::jsonb`
with a CHECK constraint enforcing `current ∈ {source_download,
dependency_restore, image_build, security_scan, snapshot_prepare,
readiness}`. The column is owned entirely by the new
`pkg/state.Store.AppendDeploymentStage` — handlers never write it
directly. The closed enum is enforced at the database layer so a
rogue writer can't insert an out-of-vocabulary stage.

Migration: `migrations/00302_deployments_stage_state.sql`
(append-only; mirrors the `00286` / `00287` jsonb+CHECK pattern).
The slot was renumbered twice during review: first 00288 → 00296
to clear main's `00296_reserve_slot.sql` reservation fence for
PR #986 (ADR-120 domain doctor, main `b3d4cf7c`), then
00300 → 00302 to clear a transient cross-PR collision with
PR #984 (issue #977 deployment annotations, ADR-116) which had
itself renumbered to slot 00301 — see
[[cross-pr-slot-precheck-pr-867-collision-2026-08-13]].

### AppendDeploymentStage semantics

```go
func (s *Store) AppendDeploymentStage(ctx, id, from, to StageName, at time.Time, reason string) (Deployment, error)
```

Three cases:

1. **Forward transition** (`from != to`): append a completed row
   for `from` to `history` (with `DurationMs`), then flip
   `current = to` + `current_started_at = at`.
2. **Failure stamp** (`from == to`): mutate the most-recent
   `history` entry's `status` to `"failed"` + set `reason`.
   Used by builderd's `markFailed` to surface build-time
   failures on the customer's deploy view.
3. **No-op** (state already terminal): caller decides; the
   function never invents state.

The implementation is a Go-side read-modify-write — matches the
existing `UpdateDeploymentStatus` race posture in `pkg/state`.
A JSONB-merge SQL alternative was considered and dropped: it
tripled the SQL surface area without buying a race fix (the
caller still needs the from→to contract), and the per-call cost
of one indexed lookup is negligible compared to the build itself.

### Emission point

Extend the existing 2s `statusTicker` loop in
`cmd/apid/handlers_ext.go:4156-4157`. The same poll that already
emits `event: status` now also diffs `stage_state` against a
per-connection `announced map[StageName]string` and emits
`event: stage` for each new entry. No typed `WakeEvent` struct
is added — the wake system stays focused on durable wake
semantics; this is ephemeral per-connection progress.

The 2s polling grain already exists; the ±2s jitter is invisible
against stages measured in seconds. Late subscribers (SSE
reconnect mid-deploy) get a full replay of `history` on their
first poll — the `announced` map's per-connection state plus the
byte-equality short-circuit on `stage_state` raw bytes ensures
no double-emit.

The dedicated `announced` map uses a 3-state tracker
(absent / "in_progress" / "completed") because a stage needs
both an in_progress frame AND a terminal frame across the
connection's lifetime — a boolean conflates the two and drops
the terminal frame.

### CLI render mode

**Live ticker with static fallback.** When `output.Enabled()` is
true (stdout is a TTY, `--json` is off, `NO_COLOR` is unset), the
CLI re-renders the 6-row block on every `event: stage` frame
using ANSI cursor positioning (`CSI Pn A` + `CSI 2 K`). When
`output.Enabled()` is false, the renderer prints one line per
frame as it arrives (no redraw).

A new `cmd/gregale/output.go` helper exposes this — currently
the file has only one-shot printers (`PrintOK`, `PrintProgress`,
…). The new helper is `output.NewLiveTicker(w, rows)` returning
a small `LiveTicker` interface with `Update(rowIdx, name,
status, dur string)` and `Close()` methods. The renderer maps
each `event: stage` payload to a row update via
`cmd/gregale/deploy_stages.go::stageTicker.HandleStageFrame`.

The TTY-vs-static branch is encapsulated in `output.NewLiveTicker`
itself — the call site is identical regardless of mode. A future
dashboard that wants the same block can drop `LiveTicker` in
front of a virtual DOM and the wire-decoding layer is unchanged.

### PR scope

Single PR. The migration fence + real migration + server schema
+ store interface + imaged/builderd chokepoints + apid SSE
emission + CLI ticker + ADR + tests all ship together. The
customer-facing UX (the ticker) only makes sense once the SSE
frame is live end-to-end; a multi-PR split would leave a window
where the server emits a frame the CLI doesn't render (or vice
versa).

### Reuse (don't reinvent)

- `cmd/gregale/output.go:55 Enabled()` — the TTY/NO_COLOR/`--json`
  gate is the single source of truth for "should I redraw?".
- `cmd/gregale/output.go:112 writeStatus` — the one-line
  formatter the live ticker falls back to in non-TTY mode.
- `pkg/api/sse.go:111 Decoder` — already preserves `event:`
  names verbatim; `event: stage` flows through with no SDK
  change.
- `cmd/apid/handlers_ext.go:4185-4209` — the existing SSE frame
  hand-format pattern; mirror it for `event: stage`.
- `pkg/imaged/handler.go:2349 transition` — the existing
  chokepoint; add a `transitionWithStage` that calls it after
  the stage append.

## Follow-on: `gregale deploys show <id>` (post-stream summary)

### Context

The live ticker is only visible while `gregale deploy` is running.
After the SSE stream closes, the only thing left in the operator's
scrollback is the terminal `✓ Deployed. …` (or `✗ Failed. …`)
line — the per-stage timing that built confidence during the
deploy is gone. Customers want to:

- re-render the same 6-row block after the deploy finishes so
  they can paste it into a post-mortem,
- script against the closed-set stages (`jq '.history | map(.name)'`)
  without re-streaming,
- hand the deployment id to support and have the support engineer
  see the same thing the customer saw.

### Decision

Add a NEW top-level verb `deploys` (distinct from `deployment` /
`deployments`) with one subcommand `show <id>`:

```
gregale deploys show <id>          # human 6-row block
gregale deploys show <id> --json   # typed stage_state envelope
```

The verb is a fresh entry in the dispatch table + usage block +
cli_meta; no shadowing of the existing `deployment` (singular GET
with `--show-scan` / `--show-secret-scan` / `set-min-instances`)
or `deployments` (paginated list). Future read-only deploy
drill-downs (timeline, events, artifacts) land as siblings of
`show` in this cluster.

Wire shape: `GET /v1/deployments/{id}/stages` returns the raw
`deployments.stage_state` jsonb verbatim (the column IS the wire
shape — no Go-side DTO layer on the server). The handler
re-emits the bytes via `json.RawMessage`; the SDK method
(`pkg/api.Client.GetDeploymentStages`) also returns
`json.RawMessage` to avoid a `pkg/api → pkg/state` import cycle
(`pkg/state/memstore.go` already imports `pkg/api`). The CLI
unmarshals into `pkg/state.StageState` directly where the
import direction is allowed.

The renderer is `renderDeploySummary` in `deploy_stages.go` —
the SAME function the live ticker calls frame-by-frame, so the
static post-stream block and the live ticker always agree on
format. Both share `stageOrder`, `stageLabels`, and
`formatStageDuration`.

### IDOR posture

Cross-account probes return **404 not 403**, mirroring
`getDeployment` (`handlers_ext.go:1136`) and `getDeploymentScan`
(`handlers_scan.go:58`). The "deployment doesn't exist" and
"deployment exists in another account" paths must be
indistinguishable — same status, same problem code, same body.

### Scope deliberately excluded

- No `--follow` flag on `deploys show`. Live stream lives in
  `gregale deploy` itself; the static summary is strict by
  design so `jq` and shell pipelines can rely on a stable
  shape.
- No second round-trip to fetch `deployments.status`. The
  renderer's footer (`live since` / `failed at` / `<status>
  at`) needs the terminal status + timestamp; for the v1
  companion we pass empty status so the footer prints just
  `<ts>`. A future `deploys status <id>` (or extending this
  verb) can supply the terminal state without re-rendering.

## Consequences

### Positive

- Customer-facing deploy UX matches Render/Fly/Heroku. The 6-row
  ticker with per-stage elapsed time is the affordance that
  builds confidence the platform is doing something
  understandable.
- The closed 6-stage vocabulary is enforced at three layers
  (Go const, sqlc enum, PostgreSQL CHECK) — a future contributor
  cannot add a 7th stage without explicitly extending the
  contract.
- The `announced` per-connection map is the documented seam for a
  future dashboard that wants the same wire shape — the apid
  handler is already factored for it.
- The migration is replay-safe (`select 1;` down-migration +
  drop-before-create pattern).

### Negative

- ~600 LOC across ~12 files (migration + state + imaged/builderd
  + apid SSE + CLI ticker + tests + ADR).
- 1 new migration slot (00302) + 1 ADR.
- The Go-side read-modify-write in `AppendDeploymentStage` is a
  small race window — two concurrent transitions on the same
  deployment row could lose a stage. The existing
  `UpdateDeploymentStatus` posture accepts the same window, and
  imaged's `transition` is called serially from one goroutine
  per deployment, so the practical risk is nil. A future
  JSONB-merge SQL could close the window if it ever matters.
- `cmd/apid/handlers_ext.go::emitStageDiff` adds three new helper
  functions (~80 LOC). The handler file was already on the long
  side; if a future PR grows another event type, the file should
  be split into `handlers_ext_sse.go` + `handlers_ext_deploy.go`.

### Neutral

- No new dependencies (no lipgloss/bubbletea — the redraw is
  hand-rolled ANSI escapes; matches the existing `output.go`
  pattern).
- No SDK regen needed — `pkg/api/sse.go` already preserves
  `event:` names verbatim.
- No new quota/limit. `pkg/api/limits.go` is unchanged.
- The two existing pre-existing test flakes in `cmd/gregale/`
  (`TestCmdAppScale_Min1_EchoesResidentCost`,
  `TestCapacityError_SurfacesDocsURL`) fail when run with the
  full suite due to parallel-test / global-state interference
  unrelated to this PR. They pass in isolation. Not addressed
  here — out of scope for ADR-117.

## Verification

### Unit (`make test`)

- `cmd/apid/handlers_ext_test.go::TestEmitStageDiff_AllSixStages`
  — 6 transitions, frame order, JSON shape, `announced` map
  dedup, failure stamp.
- `cmd/apid/handlers_stages_test.go::TestGetDeploymentStages_HappyPath`
  — 3 forward transitions through the store, GET /stages
  returns the exact jsonb the column carries (pass-through,
  no Go-side re-shape).
- `cmd/apid/handlers_stages_test.go::TestGetDeploymentStages_CrossAccountReturns404`
  — IDOR posture: cross-account probe returns 404 (not 403).
- `cmd/gregale/output_test.go::TestLiveTicker_TTY_RedrawsInPlace`
  — 6 Updates produce exactly 6 row lines + 10 cursor-up escapes
  + a Close flush.
- `cmd/gregale/output_test.go::TestLiveTicker_Static_OneLinePerUpdate`
  — same input with non-TTY; no ANSI escapes, ≥ 7 newlines.
- `cmd/gregale/output_test.go::TestStageGlyph_Mapping` — pin the
  per-status glyph table (✓/…/✗/·).
- `cmd/gregale/deploy_stages_test.go::TestRenderDeploySummary_AllSixCompleted`
  — happy-path render: every label present, `live since` footer.
- `cmd/gregale/deploy_stages_test.go::TestRenderDeploySummary_LiveDeployment`
  — mid-deploy render: no terminal footer, all 6 labels.
- `cmd/gregale/deploy_stages_test.go::TestRenderDeploySummary_Failed`
  — failed render: `failed at` footer, no `live since` footer.
- `cmd/gregale/deploys_show_test.go::TestCmdDeploysShow_HappyPath`
  — full wire path: httptest stub → SDK method → render → 6
  labels present.
- `cmd/gregale/deploys_show_test.go::TestCmdDeploysShow_JSON`
  — `--json` envelope shape: `current` + `history[]` with 6
  rows.
- `cmd/gregale/deploys_show_test.go::TestCmdDeploysShow_NotFoundFromServer`
  — server 404 surfaces as non-zero exit.
- `cmd/gregale/deploys_show_test.go::TestCmdDeploys_Dispatcher`
  — verb-level: empty / unknown / bad-id / no-args all return 1.
- `cmd/gregale/commands2_test.go::TestStreamDeployLogs_DrivesStageTicker`
  — full CLI path through `cmdDeployTarball` with the stage
  fixture; asserts every human-readable stage label appears in
  stdout.

### Lint (`make lint`)

- `goconst` tripwire: new string literals are limited to the
  `StageName` const block + label map. The `"event: stage"`
  literal appears once in `handlers_ext.go` (new emission site)
  + once in tests = under the 3-occurrence threshold.
- The leading-glyph tripwire
  (`TestLintTripwire_NoGlyphLiteralOutsideOutput`) stays green —
  the new ticker rows use space-prefixed glyphs, not
  leading-prefix glyphs.

### OpenAPI parity (`make spec-check`)

- `pkg/apid/openapi.yaml` must match `api/openapi.yaml` after
  `make spec-sync`. The new `event: stage` SSE frame is
  prose-only (the SSE vocabulary isn't in the OpenAPI schema).
  The follow-on `GET /v1/deployments/{id}/stages` route IS
  in the OpenAPI schema (closed `current` enum + `history[]`
  items); both spec copies must match.

### SDK regen (`make sdk-gen-node && make sdk-check`)

- Both Node and Python SDKs regenerate cleanly. The SSE
  vocabulary change is prose-only — no codegen impact. The
  follow-on `GetDeploymentStages` SDK method DOES add a new
  typed function (`json.RawMessage` return to avoid the
  `pkg/api → pkg/state` import cycle); SDK regen must succeed.

### Manual end-to-end

```
gregale deploy --repo OWNER/NAME --ref HEAD
```

Expect a 6-row ticker that updates as stages complete:

```
   ✓  Source downloaded         1.2s
   ✓  Dependencies restored     4.8s
   ✓  Image built               8.1s
   …  Security scan               …       ← current
   ·  Snapshot prepared           ·
   ·  Readiness passed            ·
   ✓ Deployed. https://app.gregale.dev
```

Then `NO_COLOR=1 gregale deploy --repo …` to confirm static
fallback. Then `gregale deploy --repo … --json | jq` to confirm
the JSON envelope survives (no human-readable ticker, but the
build completes with exit 0).

After the deploy finishes, `gregale deploys show <id>` re-renders
the same 6-row block from the persisted `stage_state` jsonb:

```
$ gregale deploys show 0123456789abcdef0123456789abcdef
   ✓  Source downloaded         1.2s
   ✓  Dependencies restored     4.8s
   ✓  Image built               8.1s
   ✓  Security scan             2.1s
   ✓  Snapshot prepared         1.8s
   ✓  Readiness passed          0.4s
   Total                        7.8s
   live since 2026-08-19T12:34:56Z

$ gregale deploys show <id> --json | jq '.history | map(.name)'
[
  "source_download",
  "dependency_restore",
  "image_build",
  "security_scan",
  "snapshot_prepare",
  "readiness"
]
```

### Metal-lima

**Not required.** This PR does not touch `pkg/fcvm`,
`pkg/netns`, or any metal-only path. `pkg/imaged/handler.go` is
touched at the pure-Go transition emitters only.

## Branch

- Initial PR (#985, merged 2026-08-19):
  `worktree-feat-deploy-stage-progress` (single PR, single
  branch — mirrors `worktree-feat-deploy-ux-mega-a` pattern).
- Follow-on PR (post-stream summary companion, this ADR
  amendment): `worktree-feat-deploys-show-summary` (single
  PR, ~600 LOC, no new migration slot — reuses 00302's
  `stage_state` column, adds 1 new HTTP route + SDK method +
  CLI verb + ADR amendment + tests).

## Estimated scope

Initial PR (~600-700 LOC across ~12 files: migration + state +
imaged/builderd + apid SSE + CLI ticker + tests + ADR), one new
migration slot (00302), no new deps, no SDK breakage.

Follow-on PR (~600 LOC across ~6 files: apid handler + tests +
SDK method + CLI verb + tests + ADR amendment), no new
migration slot (the 00302 `stage_state` column is the wire
shape — the follow-on PR is a read-only surface over it), one
new HTTP route, one new SDK method, no new deps.

## Post-stream v2 (deploys status + dashboard widget)

Mirrors the `pr-d-cert-engine-real-mint-2026-08-18.md` ADR-amendment
pattern (doc-only follow-on naming a new surface that consumes
existing wire contracts). Two surfaces ship together in one
mega-PR:

### A1 — `gregale deploys status <id>` + `--status` flag

The §Scope deliberately excluded bullet at line 264-265 explicitly
named the future `deploys status <id>` (or extending this verb)
as the v2 follow-on. A1 closes it:

- `gregale deploys show <id> --status` — adds the `--status` flag
  to the existing `show` subcommand. Fans out via
  `golang.org/x/sync/errgroup` over two wire calls:
  - `GET /v1/deployments/{id}` (existing) — supplies `Status`
    + `CreatedAt`.
  - `GET /v1/deployments/{id}/stages` (existing, PR #994) —
    supplies the closed-6-stage `state.StageState`.
- `gregale deploys status <id>` — same fan-out, always with the
  terminal-status footer. Pinned for ticket parity with the
  customer-facing "where is my deploy?" question.

Both surfaces pass the resulting `(Status, terminalAt)` to
`renderDeploySummary` → `pkg/dashboard/stages.RenderSummaryText`
(no duplication of the renderer). The footer branch is
status-driven:

- `status == "live"` → `live since <ts>` where `ts` is the
  first history row's `StartedAt` (all 6 stages completed for a
  live deployment).
- `status == "failed"` → `failed at <ts>` where `ts` is the
  failed row's `EndedAt`.
- `status == "superseded"` (or anything else) → `<status> at <ts>`
  where `ts` is the deployment's `CreatedAt` (the moment the
  replace-deploy landed).

### A2 — Dashboard stage timeline widget

The §Consequences positive bullet at line 279-281 explicitly named
the `announced` per-connection map as the documented seam for a
future dashboard. A2 closes it without going through the SSE
map — the post-stream summary is a static read, not a live
subscriber, so the same `state.StageState` jsonb column is the
right source.

- `pkg/dashboard/stages/` (NEW) — the shared renderer that
  opens the closed-6-stage vocabulary to the dashboard.
  Exports `StageOrder()`, `StageLabels()`, `Glyph(status)`,
  `FormatStageDuration()`, `StageOrderClosedSet = 6`,
  `RenderSummaryText(w, ...)`, `RenderSummaryHTML(...)`. The
  panic-on-drift guard fires in both Render functions when
  `len(StageOrder()) != pkg/state.AllStageNames` — same
  invariant as the live ticker.
- `pkg/dashboard/dashboard.go::DeploymentDetailData` — gains
  `Stages *StagePayload` field. `StagePayload.BodyHTML` is
  pre-rendered at the handler edge (no FuncMap wiring; the
  template only inlines `{{ with .Data.Stages }}{{ if .BodyHTML }}
  <section class="stage-timeline">…</section>{{ end }}{{ end }}`).
- `cmd/apid/handlers_dashboard.go::dashboardStagePayload` —
  mirrors `dashboardScanPayload` (the existing handler-edge
  projection pattern). Reads `d.StageState` from the same
  authorized row returned by `DeploymentByID`; no extra fetch.
- `pkg/dashboard/templates/deployment_detail.html` — inserts
  the `<h2>Stages</h2>` + `<section class="stage-timeline">`
  block between the error-explanation section and the existing
  `<h2>Scan</h2>` heading. CSS scoped under `.stage-timeline`
  (mirrors `.error-explanation`).

### What v2 does NOT change

- **No new migration** — the 00302 `stage_state` jsonb column
  is the wire shape; both surfaces read it.
- **No new column** — the footer timestamp is derived from
  `stage_state.history[*].started_at` / `.ended_at` + the
  existing `deployments.status` field. NO new `live_at` /
  `failed_at` columns.
- **No new endpoint** — A1 only consumes existing routes
  (`GET /v1/deployments/{id}` + `GET /v1/deployments/{id}/stages`).
- **No new wire vocabulary** — the SSE event names, the
  closed-6-stage names, and the per-stage status strings are
  unchanged.
- **No new SSE events** — the renderer is purely post-stream.
- **No SDK regen** — no new SDK method (both surfaces reuse
  the existing `GetDeployment` + `GetDeploymentStages`).
- **No new quota/limit** — `pkg/api/limits.go` is unchanged.

### Closed-set invariant preserved

The shared `pkg/dashboard/stages` package owns the canonical
stage order / label map / per-status glyph table / duration
formatter. The panic-on-drift guard fires at the top of both
Render functions when `len(StageOrder()) != pkg/state.AllStageNames`
or the schema CHECK in `migrations/00302` widens. The CLI
live ticker reuses the same consts via the re-export pattern
in `cmd/gregale/deploy_stages.go::renderDeploySummary` — the
dashboard cannot silently drift from the CLI's view.

### IDOR posture

A1: the wire call is 404-symmetric across both endpoints
(PR #994 review fix). A 404 on either side surfaces as the
same `printErr("Could not fetch deployment status", err)`
non-zero exit. The CLI does not distinguish "missing" from
"cross-account" — the wire is identical.

A2: `d.StageState` is read from the same authorized row
returned by `DeploymentByID` (AppBySlug + AccountID + AppID
checks). The dashboard's `http.NotFound` + `dep.AppID !=
app.ID` guards bound the read at the handler edge. No extra
fetch.

### Branch

- `worktree-feat-deploys-show-status-dashboard` (single PR,
  3 atomic commits mirroring the `deploy-ux-mega-a-shipped-2026-08-18.md`
  Mega-A pattern):
  - Commit 1: refactor + A1 — extract stage renderer to
    `pkg/dashboard/stages/` + add `deploys status` + `--status`
    flag.
  - Commit 2: A2 — dashboard stage widget on deployment
    detail page.
  - Commit 3: this ADR amendment (doc-only).

### Estimated scope

~600-800 LOC across ~12 files (1 new package
`pkg/dashboard/stages/` + 1 new test file + handler + template
+ 6 touched CLI files + 1 ADR amendment). 0 new migrations. 0
new SDK methods. 0 new wire routes. 0 new SSE events. 1 doc
amendment. 21/21 CI gates must pass per commit (matching PR
#994's green run).

## Production-ready follow-on (mega-PR after PR #1002)

PR #1002 closed the *read-side* of the closed-6-stage
vocabulary — `gregale deploys show/status`, `--status`, the
dashboard deployment-detail widget. The write-side had four
production-blocking gaps once the read-side shipped:

1. **No history retention.** `AppendDeploymentStage` is open-ended
   `append`; long-lived Hobby/Pro/Scale apps accumulate stage
   history forever. `deployments.stage_state` jsonb grows
   unbounded. Storage bound hit in months, not years.
2. **No stage error explanations.** Failed stages render as
   `"failed: <raw reason>"`. Cluster A only covers
   deployment-level codes — a customer staring at "image_build
   failed: oom" has no hint/why/fix prose.
3. **No per-stage retry.** A transient `image_build` OOM forces
   a full re-run from `source_download`. Wastes builder slots
   + tenant $$ on retries that should only re-run the failed
   stage.
4. **Dashboard stage widget is static.** No SSE wiring; opening
   `/dashboard/apps/{slug}/deployments/{id}` mid-deploy shows
   nothing live. Reads as broken on a 2-minute deploy.

This follow-on closes all four. Closed-vocabulary stays closed
(Go const + sqlc enum + migration 00302 CHECK are unchanged).

### C1 — Retention cap (64 entries)

`pkg/state/types.go::MaxStageHistory = 64` const. FIFO trim
happens Go-side in `pgstore.AppendDeploymentStage` and
`memstore.AppendDeploymentStage` at the existing read-modify-
write site (no SQL CHECK on jsonb_array_length — the nested
path is fragile across jsonb mutation shapes). Doc-only
migration at slot 00367 documents the cap; schema unchanged.

Trim is irreversible: rows past the cap are gone. No
archival. A future PR can change the cap by bumping the const.

### C2 — Per-stage retry

`POST /v1/apps/{slug}/deployments/{id}/retry` taking
`{from_stage: <StageName>}`. Inserts a fresh `deployments` row
(new id) with `stage_state.current = from_stage`,
`stage_state.history = []`. Copies input primitives
(`image`, `source_url`, `commit_sha`, `overrides`, `sidecars`,
`scope`, `traffic_percent`) from the failed row. Reuses the
existing `CreateDeployment` supersede logic at
`pkg/state/pgstore.go:4187-4244`.

`from_stage` is validated against the closed-6 vocabulary
before insert (400 on unknown). The CLI gets
`gregale deploys retry <id> [--from=<stage>]` (default = last
failed stage). The dashboard gets a per-row "Retry from this
stage" form button on failed rows (Commit 4).

A `from_stage` of `source_download` re-runs the whole pipeline
— this is intentional; that's how a user "retry from the top"
works. CLI help text calls this out.

### C3 — Stage error taxonomy

~10-15 `CodeStage*` constants in `pkg/api/errors.go` (one per
stage × 1-2 most-common failure modes). Catalog rows in
`pkg/whycopy/whycopy.go`. The existing tripwire
`TestEveryCodeHasWhycopyEntry`
(`cmd/gregale/lint_tripwires_test.go`) pins 1:1 membership
between constants and catalog rows — a new constant without a
catalog row fails the build.

The renderer (`pkg/dashboard/stages.StageFailureHTML`) takes
pre-resolved title/hint/why/fix strings so this package stays
free of `pkg/whycopy`/`pkg/api` (circular-import hazard via
`pkg/state` → `pkg/api`). The caller resolves via
`whycopy.Decorate(&problem, code, observed)` first, then
composes into the helper's positional args.

Codes shipped:

| Code | Title |
|------|-------|
| `stage_source_download_failed` | Source download failed |
| `stage_dependency_restore_failed` | Dependencies restore failed |
| `stage_image_build_oom` | Image build ran out of memory |
| `stage_image_build_timeout` | Image build timed out |
| `stage_security_scan_findings` | Security scan flagged findings |
| `stage_snapshot_prepare_timeout` | Snapshot prepare timed out |
| `stage_readiness_failed` | Readiness probe failed |

Closed-set guard at the detection site mirrors cluster A:
the imaged handler must stamp one of these codes on the
`deployments.ErrorCode` column when a stage fails, otherwise
the renderer degrades to the bare-reason fallback (no
empty `<p>` shells leak through — pinned by
`TestStageFailureHTML_EmptyDecorationsFallbackToReason`).

### C4 — SLO histograms

New `*_deploy_stage_duration_seconds{stage=,status=}`
`HistogramVec` in `pkg/wire/metrics.go` mirroring the
`wakeLatencyByPhase` pattern at `pkg/gateway/metrics.go:628-639`.
Buckets: `0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300` (skew to the
long tail so a stalled `image_build` surfaces as a top-bucket
observation rather than `+Inf`).

The (stage × status) Cartesian is pre-instantiated at boot so
`/metrics` surfaces zero on first scrape — the §12 panel
needs zero-bucket observations to render p95/p99 without
"no data". Pinned by
`TestOpsMetrics_ObserveDeployStageDuration`.

Observation sites: end of `transitionWithStage`
(`pkg/imaged/handler.go:2469`, status=completed) and end of
`MarkDeploymentStageFailed` (status=failed). The nil-receiver
guard means the call site is unconditional.

### C5 — Dashboard SSE for live stage updates

The dashboard deployment-detail page subscribes to the
existing `GET /v1/deployments/{id}/logs` SSE channel
(`cmd/apid/handlers_ext.go:4350-4416` `emitStageFrame`). New
`renderDeploymentDetailStagesPartial` handler returns the
timeline fragment only; HTMX swap-in via
`hx-trigger="sse:stage"` on the `.stage-timeline` block.
Same auth chain, same IDOR posture (read-only on the
authorized row).

SSE backstop: the existing 10-min `streamDeploymentLogs`
backstop timer means a long-running deploy may disconnect;
htmx-ext-sse reconnects transparently. A dedicated reconnect
loop is a follow-up.

### Closed-vocab invariant preserved

The closed-6-stage vocabulary stays closed at all three layers
(Go const, sqlc enum, migration 00302 CHECK). C3's
`CodeStage*` codes are a closed set in `pkg/api/errors.go`,
mirrored 1:1 in `pkg/whycopy/whycopy.go` via the existing
tripwire.

C2's retry accepts any `from_stage ∈ closed-6` (a `source_download`
retry re-runs the whole pipeline — see C2 above).

C1's trim is history-cap, not vocabulary-cap.

### IDOR posture

C2/C5 reads/writes go through the same authorized row path.
The retry handler validates `from_stage ∈ closed-6` before
insert (400 on unknown). The dashboard SSE partial handler is
inside the dashboardChain (`cmd/apid/server.go:1539`
precedent); `dep.AppID != app.ID` guard unchanged.

### Branch

- `worktree-feat-deploys-stages-prod-ready` (single PR,
  4 atomic commits):
  - Commit 1: C3 (error taxonomy) + C4 (SLO histogram) + this
    ADR amendment (doc-only). The metric test pins the
    pre-instantiation surface.
  - Commit 2: C1 (retention). Doc-only migration at slot
    00367; trim happens Go-side at the existing read-modify-
    write site. pg + memstore tests pin the FIFO trim.
  - Commit 3: C2 (retry endpoint + CLI). One new apid route +
    one new SDK method + one new CLI subcommand. Migration
    slot precheck via the cross-PR fence pattern (slot 00330
    reservation; main has likely moved past 00367 per recent
    slot-dance chains — slot renumbered eight times
    (00330 → 00346 → 00348 → 00352 → 00353 → 00356 → 00358
    → 00362 → 00367) during rebases as adjacent PRs landed
    real migrations at 00346 (issue #977 annotation, PR #984),
    00357 (PR #990 ADR-117 PR-C app_secret_value_hash), 00358
    (PR #1005 api-contract-diff deployment_openapi_snapshots),
    00360-00361 (PR #1006 SAFE-RELEASES deployment_audit +
    backfill), 00362-00366 (PR #1017 alert presets catalog —
    5 real migrations, second rebase), and 00367 is the next
    free slot above all open PRs.
    00347 (PR #1005 api-contract-diff).
  - Commit 4: C5 (dashboard SSE) + per-row retry button.
    HTMX swap-in against the existing logs SSE channel.

### Estimated scope

~1000-1300 LOC across ~25 files (4 new files: metric test,
retry handler, CLI retry, stages-partial handler; +21
touched files). 1 new migration (slot 00367, doc-only).
~10-15 new `CodeStage*` constants + matching `pkg/whycopy`
rows. 1 new Prometheus histogram + label set. 1 new SDK
method (`RetryDeploymentFromStage`). 1 new apid route
(`POST /v1/apps/{slug}/deployments/{id}/retry`). 1 new
dashboard route (`GET .../stages-partial` for HTMX SSE
swap). 1 new CLI subcommand (`gregale deploys retry`). 1 ADR
amendment (this section). 25/25 CI gates must pass per
commit (matching PR #1002's green run).
