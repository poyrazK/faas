# ADR-095 · PR-preview environments (issue #272)

- **Status:** PR-A through PR-C shipped; stable PR feedback comments are now shipped
- **Date:** 2026-08-11
- **Issue:** #272
- **Supersedes:** none.
- **Related:** ADR-012 (GitHub App + `pkg/githubd/checks.go`),
  ADR-048 (deployment.kind metering), ADR-050 (push dispatch),
  ADR-091 (named-envs / per-deployment scope, the `scope`
  field that previews reuse), ADR-092 (headless source-ref
  deploy, the same `EnqueueBuild` seam that previews will
  hit), ADR-094 (pgxpool boot warm-up), spec §4.1.2.8b
  (edge rules / preview placement), spec §14 M8 (preview
  environments).

## Context

PR-D (`10eda408`, issue #739) shipped push-to-deploy end-to-end.
PR #843 (issue #270) shipped the explicit-CI
`faas-deploy-action`. Both put `DeploymentKindGitHub` on the
deployment row and target the **production** app slug — every
push to `main` and every explicit-CI build deploys to the
customer's live URL.

Issue #272 is the missing third leg: **ephemeral PR preview
environments**. A customer opens a PR against `main`; we
provision a separate, isolated app named `pr-{N}-{slug}`,
deploy the PR head SHA into it via the existing
`POST /v1/apps/{slug}/deployments/source-ref` endpoint,
route `pr-{N}.{slug}.apps.gregale.dev` to that preview
app, post a `gregale-preview` Check Run back to GitHub
with the preview URL, and tear the preview app down when
the PR closes (or after a configurable TTL if no one
visits).

Today the webhook half of the GitHub deploy pipeline has
every primitive this slice needs:

- Live webhook receiver (`pkg/githubd`).
- Install-token mint (`StreamSourceRef` in
  `pkg/githubd/grpc.go`).
- Existing `POST /v1/apps/{slug}/deployments/source-ref`
  handler (`cmd/apid/handlers_source_ref.go`).
- Checks API writer (`pkg/githubd/checks.go`).
- Per-app scope concept introduced by ADR-091
  (`LiveDeploymentForScope` in `pkg/state/pgstore.go`).

The preview slice is the **first consumer of those
primitives** — it exercises the full pipeline with a
non-production consumer and unlocks everything after it
(preview-aware dashboard, fork-PR isolation, tier-A
PR-feedback loop). The PR feedback loop requires
`Issues:write` on the GitHub App for the single
idempotent preview status comment; fork PRs remain refused
without a per-install override (D3).

## User-confirmed design decisions (D1–D4)

- **D1 · Preview URL shape:** Per-PR subdomain
  `pr-{N}.{slug}.apps.gregale.dev` where `{slug}` is the
  parent app slug and `{N}` is the PR number. No
  path-based routing (the wake path is `/`, period), no
  per-PR hostname aliases, no `*.preview.apps.gregale.dev`
  wildcard. The DNS record is a single wildcard
  `*.apps.gregale.dev` and `pgRouter.slugFor` learns to
  peel the `pr-{N}-` prefix. The convention matches
  Vercel/Netlify previews enough that customers won't need
  docs.

- **D2 · Preview identity:** Separate `apps` row per PR.
  Every preview is a real, full-fledged app with its own
  deployment rows, its own scoped env vars (ADR-090
  `scope="pr-{N}"`), and its own wake queue. Real
  isolation: a preview crash cannot affect prod; a preview
  cannot read prod env vars; preview traffic cannot exhaust
  the prod `DeployedAppMax` quota. Tradeoff: each PR
  consumes one slot of the customer's `DeployedAppMax`
  (Free=1, Hobby=5, Pro=25, Scale=100). For the Free tier
  that's a hard ceiling; we treat previews as **counted
  against the same quota as prod**.

- **D3 · Fork-PR security:** **Refuse all fork PRs.**
  When the PR head repo fork differs from the base repo
  (the only safe definition; we cannot reliably detect
  org-private forks from webhook payload alone), the
  webhook handler short-circuits with `200 OK` (so GitHub
  doesn't retry), posts a `gregale-preview` Check Run
  with `conclusion: neutral` and `text: "Preview skipped
  for fork PR (security policy)"`, and does not provision
  an app row. The customer can request a per-repo
  override later via an ADR-track flow; for v1 the policy
  is uniform and refuse-only.

- **D4 · Preview caps:** `DeployedAppMax` only. **No**
  extra `PreviewMax` per-customer cap, **no** per-app
  preview cap. A Hobby customer (5 deployed apps) gets 5
  simultaneous previews; a Pro customer gets 25; Scale
  gets 100. The reasoning: the quota is the natural
  ceiling, adding another cap would surprise the
  customer, and the teardown janitor (PR-C) reaps stale
  previews aggressively. If a customer hits
  `DeployedAppMax`, the webhook returns the same
  `429 deployed_app_capacity` Problem the prod path
  returns and the Check Run annotation carries the
  upgrade hint.

## Three alternatives (preview identity)

| Axis | Option 1 (separate apps row) — chosen | Option 2 (label on existing app) | Option 3 (PR-only git worktree, no apps row) |
|------|----------------------------------------|----------------------------------|----------------------------------------------|
| Code change size | Moderate. New migration (slot 00220), four new app columns, handler extension. ~12 files. | Small. Add one `preview_*` boolean column. ~3 files. | Largest. New `pr_worktree_path` column, new wake path that resolves to a worktree, schedd placement changes. ~25 files. |
| Isolation | Strongest. Separate row, separate cgroup, separate scope, separate env vars. | Weakest. Same row → prod secrets visible to preview. Same scope → preview can't have preview-only env vars. | Mixed. File-level isolation only; secrets + wake queue + cgroup still shared. |
| Wake latency | Same as prod (<350 ms). | Same. | Potentially slower (worktree bind mount). |
| Spec compliance | Clean. Reuses `apps` invariants from §6.2 (≤ max_concurrency, ≤ 47,600 MB RAM, has live snapshot OR rootfs). | Violates §6.2 (single row means prod + preview share the same quota row). | New invariant needed — `pr_worktree_path` lifecycle vs `apps.id`. |
| Dashboard UX | Strongest. Existing app detail page renders preview list via partial index `apps_preview_of_slug_idx`. | Weakest. Conditional rendering on every page. | Moderate. New "PR worktrees" page. |
| Teardown cost | Strongest. `tombstone apps row + drop cgroup + drop snapshot` reuses the existing tombstone path. | Weakest. State must be unwound per preview commit on the same row. | Moderate. New worktree reaper. |

## Three alternatives (fork-PR security)

| Axis | Option 1 (refuse all fork PRs) — chosen | Option 2 (build but no secrets) | Option 3 (per-install toggle) |
|------|------------------------------------------|--------------------------------|-------------------------------|
| Code change size | Small. Single `IsFork()` check in `handlePullRequest`. | Moderate. New "preview secrets" surface + audit log + dashboard pane. | Small. One bool column on `github_installations`. |
| Security posture | Best. No untrusted code ever reaches a build VM. | Mixed. Untrusted code can still run; secrets guard is the only barrier. | Per-install opt-in. |
| ADR-012 alignment | Clean. §11 least-privilege forbids one customer's PR from running untrusted code under another customer's install. | Adds a new §11 surface (secret scoping at build time). | Uses the narrower `Issues:write` comment permission; fork execution remains refused. |
| DX | Worst for fork authors. They get a `gregale-preview: skipped` check instead of a live URL. | Best for fork authors. | Best for fork authors on opted-in installs. |
| Rollback cost | Trivial. Disable the fork check + ship the build path. | Harder. Must roll back the new secrets surface. | Same as option 1. |

## Decision

Choose **Option 1 for both preview identity (separate apps
row)** and **fork-PR security (refuse all fork PRs)**.

Concretely:

1. **D1** — `pr-{N}.{slug}.apps.gregale.dev` is the
   canonical preview URL. The wildcard cert
   `*.apps.gregale.dev` already covers this hostname;
   PR-B wires `pgRouter.slugFor` to peel the `pr-{N}-`
   prefix back off the slug.
2. **D2** — `pr-{N}-{slug}` is a fresh `apps` row. The
   `preview_of_slug` column names the parent (informational;
   no FK, since the parent may be deleted while previews
   are still active). `preview_pr_number` is stable across
   synchronize / reopened events on the same PR.
3. **D3** — Fork PRs short-circuit at the dispatcher. No
   `apps` row is provisioned. A `gregale-preview` Check
   Run with `conclusion: neutral` is the only outbound
   signal.
4. **D4** — Previews count against `DeployedAppMax`. No
   additional quota. The teardown janitor reaps
   aggressively so quota pressure is bounded by 7-day
   default TTL.

## Schema (slot 00220)

```sql
ALTER TABLE apps
    ADD COLUMN preview_of_slug    TEXT        NULL,
    ADD COLUMN preview_pr_number  INTEGER     NULL,
    ADD COLUMN preview_pr_state   TEXT        NULL
        CHECK (preview_pr_state IN ('open','closed','stale','torn_down')
               OR preview_pr_state IS NULL),
    ADD COLUMN preview_expires_at TIMESTAMPTZ NULL;

CREATE INDEX apps_preview_of_slug_idx
    ON apps (preview_of_slug)
    WHERE preview_of_slug IS NOT NULL;

CREATE INDEX apps_preview_expires_at_idx
    ON apps (preview_expires_at)
    WHERE preview_expires_at IS NOT NULL;
```

The CHECK constraint on `preview_pr_state` follows the
"Every state column has a CHECK" rule from `CLAUDE.md`.
The closed → stale → torn_down transitions are driven by
`cmd/schedd/janitor_preview.go` (PR-C). The `kind` column
on `deployments` / `builds` gains a `'preview'` value via
the same migration; without that loosening, the apid
bridge INSERT trips 23514.

## Teardown state machine

```
opened --PR closed--> closed --24h (TTL elapsed)--> stale --next tick--> torn_down
                  ^                                       |
                  |---PR reopened-------------------------|
                  |                                       |
                  +--TTL bumped via SQL (support only)----+

stale:     preview_pr_state = 'stale'; app row remains
           queryable. Wakes still complete (the schedd's
           existing kill-stuck watchdog reaps on stale
           after a grace tick; the customer's last-mile
           request lands before that). The dashboard
           surfaces the chip as red.

torn_down: preview_pr_state = 'torn_down' AND apps.status
           = 'deleted' (the existing soft-delete path,
           reused from prod). preview_url returns 410
           Gone; the slug is free for reuse.

TTL:       The 24h grace is tracked via preview_expires_at
           itself (provisioned at open time as
           created_at + 7d, refreshed on every sync /
           reopened event). The janitor treats a row as
           "past grace" iff preview_pr_state IN
           ('closed','open') AND preview_expires_at < NOW().
           A support-pushed TTL bump re-stamps
           preview_expires_at to extend the window.
```

The 24h grace between `closed` and `stale` lets a
customer reopen the PR without losing the deployment.
After `stale`, the preview is gone forever and a new PR
with the same number gets a fresh build (the slug is
`pr-{N}-{parent-slug}`, which is stable across PR events).

## Risks + mitigations

- **R1 · Wildcard cert rotation breaks preview DNS.** The
  cert at `*.apps.gregale.dev` covers previews for free.
  Wildcard renewal is already in `deploy/terraform/`.
- **R2 · Preview app row outlives the customer account.**
  When an account is deleted, the cascade deletes owned
  `apps` rows. The teardown janitor sees
  `preview_pr_state='torn_down'` and short-circuits.
- **R3 · Preview traffic exhausts `DeployedAppMax` for
  low-tier customers.** Free tier = 1 deployed app total.
  The webhook returns `429 deployed_app_capacity` and the
  Check Run carries the upgrade hint.
- **R4 · Fork-PR false positives.** D3's "head fork
  differs from base fork" check is GitHub's own
  definition; the only edge case is a same-named fork in
  a different org, which GitHub tags with a different
  `head.repo.full_name`.
- **R5 · PR number reuse.** GitHub reuses PR numbers
  after a repo transfer or a force-push. We treat the
  slug `pr-{N}-{parent-slug}` as stable; a torn-down
  PR-{N} reopens with a fresh `apps` row at the same
  slug.
- **R6 · Teardown race with an in-flight wake.** The
  janitor transitions `closed → stale` first (24h
  grace), then `stale → torn_down`. The schedd's
  existing kill-stuck watchdog reaps instances on the
  `closed` state too — a wake that lands during the
  grace period still completes. Only `stale` and
  `torn_down` refuse new wakes (410 Gone).
- **R7 · Spec drift.** The new `DeploymentKindPreview`
  enum value lands in `docs/faas_implementation_spec.md`
  §6.1.4 *before* the migration — `make spec-check`
  enforces this.
- **R8 · Check Run rate limit.** GitHub allows 1000 Check
  Run writes per install per hour. The preview path
  writes one per event; a pathological repo with 10k
  PRs/hour would hit the limit. Mitigation: the existing
  Check Run `name="gregale-preview"` dedupe means N
  pushes = 1 Check Run per PR.

## Execution order (3-PR cluster)

**PR-A · Spine** — webhook decoder + preview app creation
+ Checks API. Migration 00220. This PR.

**PR-B · Routing** — subdomain parser + gateway integration.
`pkg/gateway/router.go::previewScopeFromHost` + the
`pgRouter.slugFor` extension.

**PR-C · Teardown + UX** — janitor + dashboard + e2e.
`cmd/apid/preview_janitor.go` + the dashboard preview
panel + `cmd/e2e/preview_e2e_test.go`.

The cluster is **strictly sequential** because PR-B's
gateway parser is a no-op without PR-A's preview app
rows, and PR-C's teardown janitor needs PR-A's
`preview_pr_state` column and PR-B's routing to land
before e2e tests can exercise the full path.

### PR-C corrections vs. the original plan

Two material deviations from the PR-C plan written into this
ADR pre-PR-A:

1. **Janitor placement: `cmd/apid`, not `cmd/schedd`.**
   The original ADR text named `cmd/schedd/janitor_preview.go`.
   CLAUDE.md codifies schedd as the sole writer to
   `instances` and apid as the sole writer to customer-intent
   tables (`apps` included). The teardown writes both
   `preview_pr_state` AND triggers a soft-delete on the apps
   row; placing it in schedd would have made schedd a writer
   to `apps` for the first time and broken the ownership
   invariant. The janitor lives in apid; the tombstone emits
   `db.NotifyAppDelete` so schedd's existing app_delete
   subscriber (`pkg/sched/app_delete_subscriber.go`) reaps
   in-flight instances for the deleted app — the two pieces
   compose without a direct call.

2. **Tombstone column: `apps.status='deleted'`, not
   `apps.deleted_at`.** The original ADR referenced a
   `deleted_at` timestamp. The `apps` schema predates
   `deleted_at` (the platform's only tombstone is
   `status='deleted'`); adding a new column would have
   been a schema-drift footgun. The janitor's
   `tombstone()` calls `pkg/state.SoftDeleteAppCascade`,
   the same path the dashboard's delete button uses.

### PR-C env knobs

For tests only (cmd/e2e):

- `FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS=0` — skip the
  1-minute boot delay.
- `FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS=1` — fire every
  second (production: 5 min).

Production never overrides these. The two knobs exist
purely so the e2e can drive the full Run loop without
sleeping for minutes.

## Verification

- `make test` (all package tests pass).
- `make lint` (golangci-lint v2.4.0).
- `make sdk-check` — the new `DeploymentKindPreview`
  enum value cascades to Go / Node / Python SDKs.
- `make spec-check` — spec §6.1.4 DeploymentKind table
  gets a new row.
- `gofmt -l .` (repo-wide gate).
- PR-A gate: `TestHandlePullRequest_*` in
  `pkg/githubd/service_pull_request_test.go` (fork refusal,
  quota exhausted, idempotent synchronize, opened happy
  path, closed stamps state).

## Rollback

| Work item | Revert | Side-effects |
|---|---|---|
| Webhook decoder (`pkg/githubd/event.go`, `service.go`) | `git revert` PR-A. | No more preview app creation. Existing preview apps keep working until torn down. |
| Migration 00220 | `git revert` PR-A. | The 4 new columns drop. Preview apps created between PR-A merge and revert become orphaned. No data loss for prod. |
| Subdomain parser (PR-B) | `git revert` PR-B. | `pr-{N}.{slug}.apps.gregale.dev` URLs return 404; prod routing unaffected. |
| Teardown janitor (PR-C) | `git revert` PR-C. | Preview apps stay alive after PR closure until manual cleanup. |

The wire contract is **additive**: no existing endpoint
changes shape, no existing schema column changes type, no
existing enum value changes meaning. Rollback is purely
additive cleanup.
