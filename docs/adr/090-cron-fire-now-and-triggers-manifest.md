# ADR-090 · Manual cron fire-now and the `triggers:` manifest key (issue #791 PR-C)

- **Status:** accepted
- **Date:** 2026-08-10
- **Issue:** #791 (PR-C — closes the write side; PR-A read history = PR #795, PR-B CLI renderer = PR #806)
- **Supersedes:** the implicit "60s schedd tick is the only firer" pattern at `pkg/sched/loop.go:1712-1750` (`runCronTick`); the implicit "no manifest parser" pattern at `cmd/gregale/commands2.go:613-1026` (`cmdDeployTarball`).
- **Related:** ADR-038 (mutex on the `cron.fired` audit namespace convention), ADR-083 (cwd auto-detect shape → mirror for `gregale.yaml` discovery), ADR-085 (spec-sync required status check → openapi moves with the handler), ADR-091 (operator-obs audit namespace). `pkg/sched/loop.go:1765-1974` (`dispatchOneCron` — the canonical fire path this ADR refactors). `pkg/api/apikey.go:200-260` (scope sets). `pkg/api/limits.go:281-296` (cron quota fields).

## Context

Issue #791 listed ten gaps between the cron execution loop (fully live since launch) and the customer-facing surface. PR-A (PR #795) closed G1+G2 with `GET /v1/crons/{id}/runs`. PR-B (PR #806) closed G3 with `gregale crons runs <id>`. PR-C closes the **write side** — G4 (no manual fire-now) and G5 (no declarative `triggers:` key) — with two coupled surfaces:

### G4 — manual fire-now

`schedd` runs the 60s `cronT` ticker at `pkg/sched/loop.go:1712-1750`. The select arm at `loop.go:547-550` calls `runCronTick` which calls `dispatchOneCron` (`loop.go:1765-1974`). The boundary guard at `loop.go:1789-1798` refuses to fire unless `sched.NextFireAt(LastFiredAt or CreatedAt).After(now)`. Net: a customer debugging a cron has to wait for the next boundary (up to 60s) and never knows exactly when it is. There is no `POST /v1/crons/{id}/run` and no `gregale crons run <id>`.

The fire logic itself is correct and well-tested (`pkg/sched/cron_loop_test.go`). We do not want to duplicate it.

### G5 — declarative `triggers:` key

`cmd/gregale/commands2.go::cmdDeployTarball` (lines 613-1026) loads no manifest. The only CLI surface that creates cron rows today is the one-key provision path (`commands2.go:914-983`) — and only as a side-effect of `POST /v1/projects/scan` + `POST /v1/projects/apply`, driven by reposcan-detected workloads (k8s `CronJob`, render.yaml `cronJobs`, serverless.yml `events[].schedule`, app.yaml `jobs`/`cron`). Customers who authored a cron on a fresh project see no way to declare it as a declarable artifact alongside `services:`, `routes:`, `env:`.

There is no `gregale.yaml`/`gregale.toml` parser in the codebase. ADR-081 (`durable-execution-workflows.md`) flagged this as a "no parser today" gap in a different surface; the same gap exists for crons.

## Decision

Add two coupled surfaces, both built on the existing `POST /v1/crons` write path so the server remains the sole quota authority.

### 1. `POST /v1/crons/{id}/run` — manual fire-now

- **Route mount** in `cmd/apid/server.go` next to the existing cron block (lines 844-851). Middleware order locked: `authLimited → requireMFA → requireScope(ScopesDeployWriteSurface) → idempotent → handler`. The `idempotent` wrapper is innermost so replay lookup happens after auth/scope.
- **Handler** `cmd/apid/handlers_cron_run.go::fireCronNow` mirrors the existing two-step IDOR check (`handlers_ext.go:1576-1644`): `CronByID` → `AppByID` → `app.AccountID == acct.ID`. Cross-account and missing return byte-identical 404 bodies — no existence oracle. Disabled crons return 410 with a new `cron_disabled` stable code.
- **Scoping** rides on the existing `ScopesDeployWriteSurface` (`admin` + `deploy:write`). No new `cron:write` constant. No DB CHECK constraint change. The reasoning is in §Sub-decisions 1.
- **Reuse of schedd** is via a new exported `sched.RunCronNow(ctx, cronID, accountID string) (CronRun, error)` extracted from `dispatchOneCron`. The path below the boundary guard at `loop.go:1789-1798` is moved into a new `dispatchCronLocked(ctx, c, now, trigger)` helper. `dispatchOneCron` reduces to: parse schedule → boundary guard → `dispatchCronLocked(... "schedule")`. `RunCronNow` reduces to: load cron → enabled check → `dispatchCronLocked(... "manual")`. The deferred audit emit at `loop.go:1840-1864` is the single writer of `cron.fired*` rows — do not duplicate it.
- **Daemon bridge**: apid does NOT have a `*sched.Loop` reference — apid and schedd are separate daemons (CLAUDE.md ownership). Fire-now crosses the process boundary via a new `cron_fire_now_requests` table (`migrations/00194_cron_fire_now_requests.sql`) plus a new pg_notify channel `db.NotifyCronRunNow` (`pkg/db/notify.go`). The handler inserts a row (status `pending`) and emits the notify, then returns 202 with the request id. schedd subscribes via `SubscribeWithReconnect` (its existing wiring for `NotifyCronChanged` at `cmd/schedd/main.go:178-188`), and on each delivery `SELECT … FOR UPDATE SKIP LOCKED LIMIT 1` pulls one pending row, calls `RunCronNow`, and updates status. A 60s safety tick (next to `cronT`) handles missed notifies — the row is the source of truth, the notify is just a wakeup. This pattern matches the existing `build_queued` channel (`pkg/db/notify.go:88-90` + `cmd/imaged` consumer).
- **Why a new table, not a column on `crons`**: a column like `fire_now_requested_at` pollutes the lifecycle row with an execution-intent signal that doesn't belong there. The new `cron_fire_now_requests` table isolates the fire-now lifecycle, supports future `pause|cancel|backfill` semantics, and keeps the audit row + invocation id in one place. The `ApplyMigration` cost is one additive table + index.
- **Idempotency**: `s.idempotent(...)` (`server.go:1745-1766`) is sufficient. Replay keys on `(account, Idempotency-Key)` and returns the stored 202, so the second call never reaches `InsertFireNowRequest` and never enqueues. Propagating the key down to `EnqueueInvocation` is belt-and-braces and is out of scope.
- **Audit**: `cron.fired.manually` (new event name) with the same payload struct as `cron.fired` plus the `trigger: "manual"` field. The tick path emits `cron.fired` with `trigger: "schedule"`. One struct, two event names — see §Sub-decisions 2.
- **MarkCronFired is NOT called from `RunCronNow`** (`pkg/state/pgstore.go:5303-5313`). A manual fire must not shift `last_fired_at` — that stays owned by the tick path. The next scheduled fire still lands at the boundary.

### 2. `triggers:` manifest key

- **Loader location**: `pkg/gregalemanifest/manifest.go` (new package). Why a shared package, not `cmd/gregale/manifest.go`: `cmd/apid/scan_service.go:360-376` will later want to validate the same schema server-side, and a shared package avoids a cmd→cmd import.
- **Schema** (YAML only — `gregale.yaml` / `gregale.yml`):

  ```yaml
  triggers:
    - kind: cron
      app: my-api
      schedule: "0 3 * * *"
      path: /cleanup
      enabled: true
  ```

  Mirrors `pkg/api/dto.go:2856-2863::PlanCron` plus a `kind` discriminator. Future `kind: event|queue` slots in without a schema bump.

- **Validation** lives in `Manifest.Validate()`:
  - `kind != "cron"` → `unsupported trigger kind %q` (explicit error, never a silent skip).
  - `schedule` → `sched.ParseSchedule` (`pkg/sched/cron.go:1-62`), same validator as `validCron` (`handlers_ext.go:3537-3540`). Import it; don't re-implement.
  - `path` non-empty and starts with `/`.
  - `app` non-empty.
  - `enabled` nil → true.
  - duplicate `(app, schedule, path)` → error.

- **TOML is rejected explicitly** with "TOML manifests are not supported yet" rather than silently ignored. Decode with `KnownFields(true)` so typos fail loudly.

- **CLI integration** (`cmd/gregale/commands2.go::cmdDeployTarball`): after `CreateApp` returns the slug, before `DeployTarball`. Flow:
  1. `m, ok, err := gregalemanifest.Load(projectDir)`; `!ok` → unchanged behaviour.
  2. `m.Validate()` → abort before anything is mutated.
  3. Filter to `t.App == slug`; unknown app names → hard error listing valid slugs.
  4. Pre-count: `client.ListCrons(ctx, slug)` (`pkg/api/client.go:836-849`) + manifest count vs `Plan.CronLimitPerApp()` (`pkg/api/limits.go:2812-…`) → fail before any `CreateCron`, citing plan + limit.
  5. Fan out `client.CreateCron` (`pkg/api/client.go:850-866`) sequentially, in manifest order.

- **No `--manifest` flag** and no new subcommand — one-key provisioning (`commands2.go:914-983`) is the precedent, and a flag forks the deploy path. Add `--no-triggers` as the escape hatch.

- **Pre-count race**: the CLI pre-count is a UX fast-fail only. Authority stays with `CreateCronIfUnderQuota` (`pkg/state/pgstore.go:5177-5257`), which takes `FOR UPDATE` on the apps row, so two concurrent deploys cannot both win. The CLI renders `CronQuotaError{Scope, Limit, Observed}` (`pkg/state/store.go:141-162`) verbatim when the server rejects a clean pre-count.

- **Failure mode — fail-fast with compensation**: stop on the first `CreateCron` error; delete any rows created by this invocation before reporting both halves. The CLI keeps the staged IDs armed while upload/build/submission runs and compensates them when that deployment fails or never reaches `live`; a successful queued `--no-wait` request commits the rows. Re-running is safe because identical triples are deduped via the `(app_id, schedule, path)` key on the existing `crons` table. Cleanup uses a short context independent of the failed request's cancellation and warns if the API cannot remove a staged row.

### Sub-decisions

1. **No new `cron:write` scope.** The cron write surface already rides on `ScopesDeployWriteSurface` (`admin` + `deploy:write`). A new scope would require a closed-vocabulary addition at `pkg/api/apikey.go:109-128`, a DB CHECK constraint migration (the apikey.go::validScopes closed set is gated by `migrations/00046` and widened by `00063`), and SDK regeneration. The blast radius of fire-now is bounded by the existing two-step IDOR check + idempotency wrapper; the additional restriction does not earn its surface cost. Spec §11 treats fire-now as a side-effectful deploy; the principle is "use the existing scope unless the new operation contradicts the existing one." Fire-now does not contradict.

6. **Bridge the daemon gap via table + notify, not gRPC.** pg_notify is the existing apid↔schedd ↔imaged primitive (`pkg/db/notify.go:73-217`). gRPC between apid and schedd would couple process lifecycles (apid would 503 when schedd is down — wrong: cron writes already decouple via `NotifyCronChanged`). The table + notify pattern keeps the bridge stateless — apid can crash after insert, schedd picks up the row on its next tick.

2. **One audit payload struct, two event names.** The shared struct lives at `pkg/sched/loop.go:1840-1864` (the deferred emit). Adding `trigger` to the existing payload is the minimal change. The tick path emits `event: "cron.fired"`, `trigger: "schedule"`. The fire-now path emits `event: "cron.fired.manually"`, `trigger: "manual"`. Downstream audit-event allowlists must learn `cron.fired.manually` before merge or rows get silently dropped at the audit stage.

3. **No new scope / no DB migration / no schema change.** Both surfaces are pure wire + CLI. The cron write loop (`CreateCronIfUnderQuota`) is reused unchanged. The cron `last_fired_at` column is updated only by the tick path. The 60s `cronT` tick cadence is unchanged.

4. **Rollback**: revert the 6 commits. No DB migration. The endpoint, the SDK method, the manifest parser, and the CLI hook are all pure additions. A pure revert restores the prior behaviour (no fire-now, no manifest). The `pkg/gregalemanifest` package imports nothing destructive; deleting it leaves the CLI's pre-deploy fan-out path nonexistent (the `Load` call returns `ok=false`).

5. **OpenAPI drift**: `api/openapi.yaml` adds the `POST /v1/crons/{id}/run` path with the `Idempotency-Key` parameter (copied from the existing `POST /v1/crons` definition at `openapi.yaml:2711-2712`). The `make spec-sync` gate (ADR-085) catches drift.

### File-by-file change list

| File | Change |
|------|--------|
| `docs/adr/090-cron-fire-now-and-triggers-manifest.md` | NEW |
| `pkg/sched/loop.go` | Extract `dispatchCronLocked`; add `RunCronNow`; add `trigger` field to the shared `cron.fired*` payload struct at lines 1840-1864. |
| `pkg/sched/fire_now.go` | NEW — fire-now dispatch loop: subscribes to `NotifyCronRunNow`, polls 60s safety tick, `SELECT … FOR UPDATE SKIP LOCKED LIMIT 1`, calls `RunCronNow`, updates row status. |
| `pkg/sched/fire_now_test.go` | NEW — happy path, skip-locked concurrency, notify recovery. |
| `migrations/00193_reserve_slot.sql` | NEW — slot fence (post-rebase slot pickup). |
| `migrations/00194_cron_fire_now_requests.sql` | NEW — table + index + CHECK on status. |
| `migrations/00194_cron_fire_now_requests_test.go` | NEW — apply walk test. |
| `pkg/state/pgstore.go` | ADD `InsertFireNowRequest(ctx, cronID, accountID) (uuid.UUID, error)`, `ListPendingFireNowRequests(ctx) ([]FireNowRequest, error)`, `MarkFireNowRequestRunning(ctx, id)`, `MarkFireNowRequestSucceeded(ctx, id, invocationID)`, `MarkFireNowRequestFailed(ctx, id, err)`. |
| `pkg/state/types.go` | ADD `FireNowRequest` struct + `FireNowStatus` enum. |
| `pkg/state/queries.sql` | NEW — sqlc queries for the 5 helpers. |
| `pkg/db/notify.go` | ADD `NotifyCronRunNow` constant + payload contract doc. |
| `cmd/schedd/main.go` | Wire fire-now subscriber in the existing `SubscribeWithReconnect` block. |
| `pkg/sched/<test>` | Add `RunCronNow` tests (fires far-future cron, leaves `last_fired_at` unchanged). |
| `pkg/api/dto.go` | Add `FireCronResponse{RequestID uuid.UUID, Status string}`. |
| `pkg/api/client.go` | Add `Client.FireCron(ctx, id) (uuid.UUID, error)`. |
| `pkg/api/errors.go` | Add `CodeCronDisabled` + `ErrCronDisabled` near 1342-1360. |
| `pkg/api/client_test.go` | Add `FireCron` round-trip test. |
| `cmd/apid/server.go` | Register `POST /v1/crons/{id}/run` route near 844-851. |
| `cmd/apid/handlers_cron_run.go` | NEW — `fireCronNow` handler (insert row + notify + 202). |
| `cmd/apid/handlers_cron_run_test.go` | NEW — 202/400/404/404-byte-identical/410/503 + idempotency tests. |
| `pkg/gregalemanifest/manifest.go` | NEW — `Load`, `Validate`, schema types. |
| `pkg/gregalemanifest/manifest_test.go` | NEW — table-driven tests. |
| `cmd/gregale/commands2.go` | Wire `cmdDeployTarball` to call `gregalemanifest.Load` + `gregalemanifest.Validate` + fan-out. Add `--no-triggers` flag. |
| `cmd/gregale/manifest_test.go` | NEW — fan-out vs fake client (pre-count trip, fail-fast at entry 4, `--no-triggers` skips). |
| `api/openapi.yaml` | Add `POST /v1/crons/{id}/run` path; 202/400/404/410/429/503 responses. |
| `pkg/apid/openapi.yaml` | Regenerated via `make spec-sync`. |

## Consequences

### Positive

- G4 (fire-now) and G5 (`triggers:`) of issue #791 are closed. The issue itself (which is `CLOSED` as of PR-B's merge per the GitHub state) is now fully delivered.
- `sched.RunCronNow` is the single canonical "fire this cron" helper for both the tick path and the API path. Future cancel/pause/backfill surfaces are built on the same helper.
- The `triggers:` schema is forward-compatible — `kind: event|queue` slot in without a manifest version bump.
- Server remains the sole quota authority. The CLI's pre-count is a UX fast-fail; the `CreateCronIfUnderQuota` lock is the truth.

### Negative

- The audit payload struct gains a `trigger` field. Existing audit-event allowlists must learn `cron.fired.manually` before merge or the new event's rows get silently dropped. Documented in PR description.
- The `triggers:` schema is YAML only. TOML is rejected explicitly. A future request to add TOML would be a new ADR.
- Manifest fan-out still adds a second API write before the deployment request, so a customer can observe a short-lived staged trigger while the upload is in flight. The CLI compensates those rows on rejected/failed/unknown deployments; if the cleanup API is unavailable, it reports the incomplete rollback so the operator can remove the named trigger manually.

### Compatible

- `LastFiredAt` semantics are unchanged. The tick path still owns it; `MarkCronFired` is not called from `RunCronNow`.
- The 60s tick cadence is unchanged.
- Existing `cron.fired` audit rows keep the same payload shape minus the new `trigger` field (which is `omitempty` and absent on the row) — dashboards that key on `payload[k]` semantics are unaffected.

## Rollback

Revert the 6+ commits. The `cron_fire_now_requests` migration rolls back via goose `Down` (DROP TABLE). The endpoint, the SDK method, the manifest parser, and the CLI hook are all pure additions; a pure revert restores the prior behaviour (no fire-now, no manifest). The cron `last_fired_at` column is unchanged by this PR. The `trigger` field added to the audit payload is `omitempty` so old rows are valid under the new reader.

## Closure (issue #791 PR-E)

The original ADR's "Consequences" section claimed issue #791 was "fully delivered" but three operator-visible gaps remained after PR-D shipped. PR-E closes those gaps under the same ADR's envelope — no new ADR — because every change here is a closure of the existing decision, not a new one.

Three gaps closed by PR-E:

1. **Dashboard cron runs panel** — `pkg/dashboard/templates/app_detail.html` now renders per-cron section with `Last N runs` `<details>`, glyph-coloured `✓` / `✗` / `⟳` rows, the underlying handler pre-formats every field (`StartedAt`, `DurationMS`, `Outcome`) at handler edge so the template stays a pure renderer. The page re-renders on `sse:cron_fired` events (no polling). An inline `Fire now` form is wired for each cron, mirroring `dashboardDelete`'s CSRF envelope (review finding A3).
2. **`crons info <id>` CLI dispatch** — `cmd/gregale/commands_crons_info.go` new, `commands2.go` wires the `subInfo` arm. Local cron id regex pre-check prevents the no-server-call branch, byte-identical 404 preserves the no-existence-oracle contract.
3. **`(app_id, schedule, path)` UNIQUE constraint** — `migrations/00210_crons_unique_app_schedule_path.sql` (renumbered three times at push time: 00207 collided with PR #826 + PR #836; 00208 collided with PR #836's deployments_scope; 00209 collided with PR #836's renumbered-to-00209 deployments_scope AND PR #829's paddle-overage) enforces the tuple the manifest dedupe primitive has been asserting all along. A customer-managed `triggers:` manifest with two entries that collide on this tuple now fails fast at INSERT instead of producing a duplicate crons row.

Single new public endpoint: `GET /v1/crons/{id}` (handlers_ext.go::getCron). Same IDOR-safe two-step as updateCron/deleteCron; routed through `s.authLimited(s.requireScope(ScopesReadSurface...))`. The new CLI subcommand and the SDK method (Go: `GetCron`) flow through this endpoint with zero special-casing — the surface is uniform across `crons info`, `Gregale\cron::Get`, and any future dashboard drill-down.
