# ADR-124 · Deployment queue controls (cancel / reorder / deploy-immediately / clear obsolete)

- **Status:** accepted
- **Date:** 2026-08-21
- **Issue:** follow-up to ADR-089 §35 (deferred `'cancelled'` status) + issue #961 follow-on (deploy UX, Mega-C PR-2)
- **Supersedes:** the implicit "deployments are a fire-and-forget pipeline; once the row leaves `pending` the user has no handle on it" pattern that ADR-089 `build-status-endpoint.md:35,90` explicitly defers: *"Adding `cancelled` requires a separate migration + builderd path + a `--cancel` CLI flag. Out of scope for this PR; a follow-up ADR can extend the CHECK if/when the cancel path lands."*
- **Related:** ADR-089 (build status, the predecessor endpoint), ADR-117 (deploy stage progress — the canonical read surface we're extending), ADR-118 (AutoRollbackDeploymentsTx — the single-tx "swap live ↔ superseded" pattern we mirror for the cancel orchestrator), ADR-099 (wake admission queue control, capacity knobs), ADR-072 (set-min-instances — the in-place PATCH-deployment precedent), ADR-041 (migration slot discipline).

## Context

After Mega-C PR-1 (PR #987, `gregale deploy now ships`) and Mega-C PR-3 (PR #990, env-diff), the deploy UX has every "see what's happening" primitive a customer needs:

- `gregale deploys show <id>` — full row + 6-stage jsonb timeline (PR #984, ADR-117).
- `gregale deployments --limit N --before CURSOR` — paginated list (`cmd/gregale/commands_deployments.go:122`).
- `gregale deploys status <id>` — render the 6-stage timeline as terminal text (`cmd/gregale/deploys_show.go:198`).

It has zero "control what's happening" primitives. Once a deployment row leaves `DeployPending`:

- The Firecracker build VM is created by `VMMDriver.Spawn` (`pkg/builderd/vm_metal.go:119-215`), awaited by `WaitForCompletion` (`:229-307`), and the only exit is natural completion or `SweepStuckRunningBuilds` (`pgstore.go:5805`). There is **no `Cancel`** path.
- The state machine's only terminal exits from non-live states are `Failed` (worker error) and `Superseded` (new live row landed). A user who pushed the wrong branch, picked the wrong railpack vs. dockerfile, or simply changed their mind cannot retract.
- A "deploy immediately" expectation exists for paid plans but is unsupported — `Spec §5 §6` promise FIFO + per-app serialisation (`pkg/state/pgstore.go:4179-4255`), but no user priority column.
- `DeploySuperseded` rows accumulate forever (`pgstore.go:4693-4722` supersede-only, no GC path).

This ADR ships the four controls that close the loop: cancel, reorder (via `priority`), deploy-immediately (priority=0 shorthand), and clear-obsolete.

### Decisions

1. **`'cancelled'` is its own terminal `DeploymentStatus` and `BuildStatus` value** (additive CHECK widening). Mirrors ADR-089's deferred pattern. Keeps the audit trail separate from `DeploySuperseded` (cancel-of-mine vs. natural-supersede-by-newer-deploy must stay distinguishable in WAL history).
2. **Live deployments (`status='live'`) cannot be cancelled** — return `CodeDeploymentCancelLiveForbidden` (HTTP 409) with a fix hint pointing at `gregale deploys rollback`. Cancelling live would either park the app (kills INV 3 "always has live snapshot OR cold-bootable rootfs") or scale-to-zero (kills INV 4 "parked consumes zero RAM"); the deploys-rollback path (ADR-118) is the user-correct escape.
3. **Reorder / deploy-immediately / priority via an additive `priority INT NOT NULL DEFAULT 100` column on `deployments`**, claimed by builderd via `ORDER BY d.priority ASC, b.enqueued_at ASC`. Range `[0,1000]`: 0 = deploy-immediately (UI "↑"); 100 = FIFO (the prior default); 1000 = background rebuild.
4. **Build-cancel requires a new VMM RPC** — `pkg/fcvm.Manager.CancelBuild(ctx, buildID)` maps to `Manager.Destroy(ctx, "build-"+buildID)` (existing primitive at `pkg/fcvm/manager.go:2815`). Builderd's cancel goroutine LISTENs on `NotifyDeploymentChanged`, races the build row to `'cancelled'`, then issues the RPC best-effort. `SweepStuckRunningBuilds` (`:5805`) is the safety net.
5. **Plan gate**: Free = cancel + clear-obsolete enabled; Hobby/Pro/Scale = all four controls enabled. Mirrors the TriggersAllowed/Free-tier precedent at `pkg/api/limits.go:702`.
6. **Soft-delete via `deleted_at` + `deleted_by_principal`**, separate axis from `cancelled_at` + `cancel_reason`. A cancelled row keeps its audit trail visible; a cleared row is hidden from the customer list but visible to admins.
7. **`pg_notify` reuse**: deployment_changed carries the new `status="cancelled"` payload (existing schema at `pkg/db/notify.go:74-84` already accepts free-string status). Build cancel adds a new `NotifyBuildChanged` payload `{build_id, status, deployment_id}` mirroring the deployment side.

## Decision (wire vocabulary + storage)

### Migrations

- `migrations/00362_deployments_cancelled.sql` — widens `deployments_status_check` to add `'cancelled'`; adds `cancelled_at`, `cancelled_by_principal`, `cancel_reason` (closed-set `user|auto_quota|auto_health|system`), `deleted_at`, `deleted_by_principal`. Existing `deployments_app_idx` from migration 00001 (`app_id`, `created_at DESC`) covers the clear-obsolete query path; no new index needed.
- `migrations/00363_builds_cancelled.sql` — widens `builds_status_check` to add `'cancelled'`; adds `cancelled_at`, `cancelled_by_deployment_cascade bool NOT NULL DEFAULT false` (true when the source was `CancelDeploymentTx` vs. a future direct build-cancel path). Index `(deployment_id, status, cancelled_at DESC)`.
- `migrations/00364_deployments_priority.sql` — adds `priority int NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 1000)`; partial index `(app_id, priority, enqueued_at) WHERE status='pending'`.

Slot precheck (round-2): PR #1017 (ADR-123 alert presets) lands at 00357–00361 on its branch tip. Our renumber claims 00362–00364 (next free above its real slots). Reservation fences at 00347–00361 fill the gap from main's 00346 to our claim; these will land on main only when PR #1017 merges, leaving PR #1017's real slots to absorb the fences.

### State machine extensions (`pkg/state/types.go`)

- `DeployCancelled DeploymentStatus = "cancelled"` added to the enum at `:78-86`.
- `BuildCancelled BuildStatus = "cancelled"` added at `:199-204`.
- `CancelReason string` closed-set `"user"|"auto_quota"|"auto_health"|"system"`, mirroring the `ParkReason` precedent at `:155-194`. `IsValid()` helper included.
- `Deployment.Priority int` — projection of `deployments.priority`.

### Store additions (`pkg/state/store.go`)

New sentinels:

- `ErrInvalidStateTransition` — `MarkDeploymentCancelled` returned when the row's current status is not in `{pending, building, imaging, snapshotting}`.
- `ErrCancelLiveForbidden` — `CancelDeploymentTx` returned when the row's current status is `'live'`; the handler maps this to 409.
- `ErrReorderNotPending` — `ReorderDeployment` returned when the row's current status is not `'pending'`.

New `Store` interface methods (mirror shape of `MarkDeploymentSuperseded` at `:1808` and `LatestDeployment` at `:4370`):

- `MarkDeploymentCancelled(ctx context.Context, id string, principal string, reason CancelReason, when time.Time) error`
- `CancelDeploymentTx(ctx context.Context, id string, principal string, reason CancelReason) (Deployment, error)` — single-tx orchestrator mirroring `AutoRollbackDeploymentsTx` from ADR-118 / PR-A worktree (`worktree-feat-deploy-ux-mega-c-pr1`, commit `c76fd64e6`).
- `ReorderDeployment(ctx context.Context, id string, newPriority int, principal string) error`
- `ClearDeployment(ctx context.Context, id string, principal string) error` — sets `deleted_at` (soft-delete; `status` unchanged).
- `ClearObsoleteDeployments(ctx context.Context, appID string, olderThan time.Time) (count int, err error)` — bulk UPDATE for `superseded|failed|cancelled` rows older than the cutoff, **skipping** the most-recent N per app (N lives in `pkg/api/limits.go` and is read by the imaged nightly GC already — INV 3 compliance).

### pgstore implementations (`pkg/state/pgstore.go`)

Each new method:

- Atomic UPDATE with a single `WHERE id = $1 AND status IN (...)` guard so concurrent transitions are CAS-safe (mirrors `pgstore.go:5675-5683` build CAS guard).
- Returns the typed sentinels above via the canonical `mapErr` funnel (`:13693-13724`).
- `CancelDeploymentTx` uses `pgx.Tx` (per `pgstore.go:3738-3741` "INTERFACE-PURE — no pgx.Tx leaks" rule: the function returns the row but never the tx); `pg_notify('deployment_changed', payload)` runs from the apid path after commit (single producer).
- `pgstore.go:5081-5090` `UpdateDeploymentStatus` is **not** extended (it would weaken the CAS guard). The new methods are additive.

### Claims honour priority

`pgstore.go:5853-5940` `ClaimNextQueuedBuild` and `ClaimNextQueuedBuildWithFairness` change their ORDER BY to `d.priority ASC, b.enqueued_at ASC`. Existing FIFO claimers gain ordering "for free" because the partial index M3 covers the new key.

## Decision (API surface)

### Routes (`cmd/apid/server.go`, near `:966-1018`)

| Method + Path | Scope | Handler |
|---|---|---|
| `POST /v1/apps/{slug}/deployments/{id}/cancel` | `deploy:write` | `HandleCancelDeployment` |
| `POST /v1/apps/{slug}/deployments/{id}/reorder` | `deploy:write` + `Plan.QueueControlsAllowed()` | `HandleReorderDeployment` |
| `DELETE /v1/apps/{slug}/deployments/{id}` | `deploy:write` | `HandleClearDeployment` |
| `POST /v1/apps/{slug}/deployments/clear-obsolete` | `deploy:write` + `Plan.QueueControlsAllowed()` | `HandleClearObsoleteDeployments` |

All four reuse the `authLimited → requireMFA → requireScope(ScopeDeployWrite)` chain (precedent at `server.go:939-941, 966`). All four emit `pg_notify('deployment_changed', payload)` on success (existing producer at `pkg/apid/apidsource/apidsource.go:382`).

### Errors (`pkg/api/errors.go`)

- `CodeDeploymentCancelLiveForbidden = "deployment_cancel_live_forbidden"` → 409, `Hint: "Use 'gregale deploys rollback <id>' to swap to a previous deployment."`.
- `CodeDeploymentReorderNotPending = "deployment_reorder_not_pending"` → 409.
- `CodeDeploymentCancelNoSuchDeployment = "deployment_cancel_no_such_deployment"` → 404 (mirror `CodeBuildNotFound` at `:859`).
- `CodePlanReorderDisabled = "plan_reorder_disabled"` → 402.

All four append to the const block at `:322-770`; each gets a `NewProblem(http.StatusXXX, CodeXxx, …)` constructor at `:2665, 3446`; `StatusForCode` switch at `:1374` gains the four entries.

### SDK methods (`pkg/api/client.go`, hand-extracted — `Makefile:810-812`)

```go
func (c *Client) CancelDeployment(ctx context.Context, appSlug, id string, opts ...RequestOpt) (Deployment, error)
func (c *Client) ReorderDeployment(ctx context.Context, appSlug, id string, newPriority int, opts ...RequestOpt) (Deployment, error)
func (c *Client) ClearDeployment(ctx context.Context, appSlug, id string, opts ...RequestOpt) error
func (c *Client) ClearObsoleteDeployments(ctx context.Context, appSlug string, olderThan time.Time, opts ...RequestOpt) (ClearObsoleteReport, error)
```

`make spec-check && make sdk-gen-node && make sdk-check` (Makefile `:754-769`) enforces parity.

### Limits (`pkg/api/limits.go`, queue/event group at `:301-345`)

```go
// ADR-124 deployment queue controls
QueueControlsAllowed    bool   // Free: false. Hobby/Pro/Scale: true.
MaxQueuedDeploysPerApp  int    // Free: 2. Hobby: 5. Pro: 10. Scale: 25.
MaxCancelOpsPerHour     int    // All plans: 120.
MaxReorderOpsPerHour    int    // Free: 0 (gate). Hobby/Pro/Scale: 60.
```

Per-plan overrides in `planLimits` (`:1099`). `pkg/api/limits_test.go` updated for the new fields. `limits.go` is the **only** place these constants live per CLAUDE.md Hard limits.

### Metrics (`pkg/wire/metrics.go`, near `:1750-1768`)

Counter `deployment_cancelled_total{reason}` (`reason ∈ {user, auto_quota, auto_health, system}`). Counter `build_cancelled_total{phase}` (`phase ∈ {queued, running, deploying_vm}`). Counter `deployment_reorder_ops_total` paired with `deployment_clear_ops_total{axis}` (`axis ∈ {single, bulk}`). All four names follow the `<prefix>_noun_verb_total` convention specced in §12.

### CLI (`cmd/gregale/`)

Mirror the 5-touchpoint pattern documented at `cmd/gregale/deploys_show.go:108-425`:

- `deploys cancel <id> [--reason <r>]` (`cmd/gregale/deploys_cancel.go`, new)
- `deploys reorder <id> --priority N` (`cmd/gregale/deploys_reorder.go`, new)
- `deploys clear <id> [--force]` (`cmd/gregale/deploys_clear.go`, new) — interactive y/N confirmation; `--force` bypasses.
- `deploys clear-obsolete [--older-than <dur>] [--dry-run]` (`cmd/gregale/deploys_clear_obsolete.go`, new) — `--older-than` defaults to `168h` (7d, matches the imaged nightly GC cycle).

Help banner at `main.go:50`, dispatcher arm at `:235-242`, manifest entry at `cli_meta.go:325-339`, dispatcher const at `commands2.go:160-165`, **+ drift test** in `commands_completion_test.go::TestCompletion_ManifestDrift`.

## Consequences

### Positive

- Closes the customer-facing "what's my deploy doing?" gap — operators can retract misfired deploys without operator support tickets.
- Free plan keeps the safety valves (cancel + clear-obsolete); Paid plans get the full competitive-surfaces (reorder, deploy-immediately).
- The cancel signal fires `pg_notify('deployment_changed')` — gatewayd's live picker (`pkg/gateway/pgbackend.go:974, 1105`), schedd's loop (`pkg/sched/loop.go:441, 1095`), and imaged's loop (`pkg/imaged/loop.go:148`) all pick it up via the existing subscriber map. Zero new subscribers.
- Auto-rollback (ADR-118) and cancel-via-tx share the same single-tx shape, so a follow-up "auto-cancel on quota breach" PR has a clean precedent.

### Negative

- A new VMMDriver interface method (`Cancel`) ships **across the build tag boundary** — non-metal stub returns `ErrCancelUnsupported`. Failure modes when builderd has no VM to kill but the row already flipped (the goroutine logs + retries; janitor `SweepStuckRunningBuilds` is the durable backstop).
- `priority` admission control is at the SQL level (builderd's claim is `(priority, enqueued_at)` ordered). High-priority churn from a single account could starve others — the per-account `MaxCancelOpsPerHour`/`MaxReorderOpsPerHour` caps are the only knobs. A wake-burst equivalent for builds (mirroring ADR-099) is a follow-up.

### Neutral

- `DeploySuperseded` semantics unchanged — superseded-by-newer-deploy stays a *system* event; cancel-and-deploy-now is a *user* event. The `cancel_reason` column records the distinction in WAL.
- The pg_notify payload contract already accepts a free-string `status`; no schema change to `pkg/db/notify.go`.

## Verification

### Unit (`make test`)

- `pkg/state/pgstore_state_check_*_test.go` — add `TestMarkDeploymentCancelled_*`, `TestReorderDeployment_*`, `TestClearDeployment_*`, `TestClearObsoleteDeployments_*`. State interface tests run via memstore + skip-on-no-DATABASE_URL (Makefile `:158-162`).
- `pkg/state/pgstore_cancel_lifecycle_test.go` (new) — full happy-path: enqueue → flip to building → `CancelDeploymentTx` → row gone + build row gone + pg_notify. Includes the live-row blocked path.
- `cmd/gregale/deploys_*_test.go` — httptest-driven happy path + 3 error paths per verb (live-blocked, not-pending-for-reorder, plan-gate for Free).
- `pkg/api/limits_test.go` — every new field covered.

### Lint (`make lint`)

- New `gofmt -w` pass before push (per `gofmt-local-vs-ci-version-mismatch.md`).
- golangci-lint v2.4.0 — handler checklist compliance (`pkg/builderd/vm_metal.go:0 + Cancel method` ≤50 line blocks where the goroutine listens).
- codeql — secret/PII leakage cleared (no new logging).

### OpenAPI parity (`make spec-check`)

- `api/openapi.yaml` gets the four new routes + schema; `make spec-sync` syncs the embed.

### SDK regen (`make sdk-gen-node && make sdk-check`)

- All four `pkg/api/client.go` method signatures land; `cmd/sdk-coverage` exports 100 % coverage for the deployment surface.

### Manual end-to-end

1. Deploy an app; wait until status=building.
2. `gregale deploys cancel <id>` — expect 200; pg_notify stream shows `status=cancelled`; deployment row's `cancelled_at`, `cancelled_by_principal`, `cancel_reason='user'` are set.
3. Inspect the build row directly: `builds.status='cancelled'`, `cancelled_at` set, `cancelled_by_deployment_cascade=true`.
4. Cancel a `DeployLive` row — expect 409 `deployment_cancel_live_forbidden` with the `gregale deploys rollback` hint.
5. `gregale deploys reorder <id> --priority 0` — the row jumps to the front of the partial-index `deployments_pending_priority_idx`.
6. On a Free-plan token: `gregale deploys reorder <id>` returns 402 `plan_reorder_disabled`; `gregale deploys cancel` still works.
7. `gregale deploys clear <id>` on a superseded row: 200; row's `deleted_at` set.
8. `gregale deploys clear-obsolete --older-than 168h --dry-run`: 200, body shows the dry-run count ≥ 0.

### Metal-lima (`sudo make test-metal`)

- `pkg/builderd/vm_metal_test.go::TestCancelRunningBuild` — starts a build, mid-build issues `VMMDriver.Cancel`, asserts the row reaches `'cancelled'` within 5s; ensures no orphan VM (jailer chroot absent). Per CLAUDE.md this validates the arm64 path; a final sign-off on bare-metal x86_64 (§14 acceptance) is still required.

## Branch

`worktree-adr-124-deployment-queue-controls`, branch off `main` HEAD (currently `61e3e5779`).

## Estimated scope

- **~10 new files**: 3 migration files, ADR-124, 4 CLI leaf files (deploys_cancel/reorder/clear/clear_obsolete + their tests).
- **~16 modified files**: `pkg/state/types.go` (+1 enum type), `pkg/state/store.go` (+4 interface methods + 3 sentinels), `pkg/state/pgstore.go` (+5 impl + 1 query update), `cmd/apid/server.go` (+4 routes), `cmd/apid/handlers.go` (+4 handlers), `pkg/api/errors.go` (+4 codes), `pkg/api/client.go` (+4 SDK methods), `pkg/api/limits.go` (+4 fields, +4 per-plan rows), `pkg/wire/metrics.go` (+3 counters), `pkg/fcvm/manager.go` (+1 CancelBuild), `pkg/builderd/vm_metal.go` (+Cancel method on interface), `pkg/builderd/vm_stub.go` (+stub method), `pkg/builderd/builderd.go` (+cancel goroutine), `cmd/builderd/main.go` (+cancel goroutine wiring), `pkg/db/notify.go` (+NotifyBuildChanged), proto files (+CancelBuild RPC).
- **3 migration slots**: 00362 / 00363 / 00364 (round-2 renumber after PR #1017 grew to 00357–00361 on its branch tip).
- **~1200 LOC + 700 LOC tests**.
