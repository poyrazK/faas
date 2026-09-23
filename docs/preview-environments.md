# PR preview environments (issue #272 / ADR-095)

Every pull request against a connected GitHub repo gets its own
ephemeral app, deployed on push, routed at a per-PR subdomain,
and torn down on PR close (or after the TTL — whichever comes
first). No CI configuration required; the integration is
triggered by the GitHub App the customer installed via
`gregale connect`.

## CLI workflow

Queue a preview without keeping the terminal attached to the build, then
resume by preview slug when a later step needs the usable URL:

```bash
gregale preview create --app checkout --repo acme/checkout \
  --ref feature/shipping --pr-number 42 --no-wait
gregale preview wait pr-42-checkout --open
```

`preview wait` follows the newest deployment for the preview, so a new push
does not leave the command watching an obsolete deployment. Use `--progress`
for lifecycle transitions or `--json` for a stable receipt containing the
preview URL, expiry, deployment status, readiness, timeout resume command,
and the next diagnostic action.

## URL shape

```
https://pr-{N}-{slug}.<zone>
```

- `{N}` — GitHub PR number, stable across `synchronize` and
  `reopened` events on the same PR.
- `{slug}` — the parent app's slug (the production app the PR
  targets).
- `{zone}` — the platform zone; `gregale.dev` on the
  hosted product.

The wildcard cert at `*.<zone>` covers previews for free —
no per-PR cert provisioning, no DNS work.

## Lifecycle

| Stage | What it means |
|---|---|
| **Open** | A live preview app responding to requests. Wakes on first hit (cold-boot), scales to zero on idle. |
| **Closed** | PR was closed / merged. The preview stays live for **24 hours** so the author (or a reviewer) can revisit the URL one last time. Wakes are still served. |
| **Stale** | 24h grace elapsed. The preview app is queued for teardown on the next janitor tick. Wakes still complete. |
| **Torn down** | App row tombstoned, instances reaped, snapshot freed. The URL returns `410 Gone`. |

A reopened PR during the grace period bumps the row back to
**Open** and the URL starts serving again on the next push.

## Quota

Each preview consumes **one slot** of the customer's
`DeployedAppMax`:

- **Free** — 1 slot total (production + preview). Preview
  attempts beyond the first get `429 deployed_app_capacity`.
- **Hobby** — 5 slots.
- **Pro** — 25 slots.
- **Scale** — 100 slots.

This is the same ceiling production apps use; there is no
separate preview cap. The 7-day default TTL plus the 24h
closed-grace window plus the janitor's per-tick sweep keep
the steady-state preview count bounded — a customer who
opens 20 PRs today will not have 20 previews live a week
from now.

## Fork PRs

PRs from forks are **refused**. The webhook posts a
`gregale-preview` Check Run with `conclusion: neutral` and
`text: "Preview skipped for fork PR (security policy)"`. No
app row is provisioned. This is deliberate: we cannot
guarantee that untrusted code from a fork will not reach the
build VM without a per-install override. The GitHub App needs
`Deployments:write` for the deployment timeline and `Issues:write`
to maintain the single preview status comment on the PR's issue
thread; it does not need `Pull requests:write`.

## Dashboard

Open `/dashboard/apps/{slug}` on the production app — the
**Preview environments** panel lists every preview with its PR
number, current state (chip), the preview URL, and a Copy
URL button (writes to clipboard). A preview row on the apps
list (`/dashboard/apps`) is indented and tagged with a
`preview` chip.

## Pull request feedback

Gregale maintains one bot comment per preview PR. The comment is
updated on `opened`, `synchronize`, `reopened`, and `closed` events,
and includes the preview URL, current lifecycle status, commit SHA,
and one-click destroy link. A hidden marker makes webhook retries and
repeated synchronize events idempotent instead of creating duplicate
comments. The Check Run remains the source of build-stage status.

Each preview deployment also appears in GitHub's Deployments timeline
with a stable environment URL and deployment/log links. Status updates
reuse the same provider deployment across retries, so GitHub consumers
see one `queued` → `in_progress` → `success`/`failure` lifecycle rather
than a new deployment row for every phase.

The Deployment record also carries the provenance already stored on the
Gregale deployment row: deployer, pull-request number, tag, and optional
reason. These values are included in the Deployment description and under
`payload.gregale_*`, while the Check Run and preview comment render the
deployer, PR, and reason for reviewers without requiring a second Gregale
API lookup.

## Mocking API routes

Preview apps can serve a fixed JSON response before the backend route is ready.
Create a `respond` edge rule for the preview app, then disable or delete it when
the implementation is deployed:

```bash
gregale edge-rules create --app pr-42-checkout \
  --kind respond \
  --match-host pr-42-checkout.gregale.dev \
  --match-path /shipping/estimate \
  --match-method GET \
  --respond-status 200 \
  --respond-body '{"days":3,"price":4.99}'
```

The rule is preview-only, runs after the preview app's authentication gates, and
returns the configured status and JSON body without waking the backend. Use a
non-2xx status such as `500` to exercise the frontend failure state. Responses are
bounded to 64 KiB; status `204` and `304` cannot carry a body.

## Operational notes

- **Stuck teardowns.** If a preview sits in `closed` for
  longer than 24h + one janitor tick, check
  `journalctl -u faas-apid` for `preview janitor: tick
  failed`. The janitor is non-fatal on row errors; a single
  bad row does not abort the sweep.
- **Premature teardown.** A `closed` preview's 24h grace is
  a contract, not a knob. To keep a preview alive beyond
  grace, either reopen the PR (bumps state to `open`,
  resets TTL via the dispatcher) or — for an ad-hoc
  extension — ask support to bump `preview_expires_at`
  manually via SQL.
- **Reusing a torn-down PR number.** GitHub reuses PR
  numbers after a repo transfer or force-push. We treat
  `pr-{N}-{parent-slug}` as stable across events; a
  torn-down PR-{N} that reopens with a fresh head SHA gets
  a fresh `apps` row at the same slug.

## Project GitHub deployment policy

Customers can read and update the project policy through
`/v1/apps/{slug}/github/deployment-policy` with the `github:manage` scope.
The policy supports a repository-relative root directory for root workloads,
ignored change paths (exact paths, one-segment globs, and trailing `/**`
directory patterns), a preview enable switch, and a preview TTL from 1 hour
to 30 days. It also controls whether previews can call production internal
services. New projects default to `preview_service_policy: deny`; projects
that existed when the policy shipped were migration-backed to `allow_marked`
to avoid changing live traffic. All projects otherwise default to previews
enabled, a 7-day TTL, no ignored paths, and the repository root.

When all changed files match ignored paths, githubd records the delivery as a
successful no-op and does not enqueue builds. Compare-API failures still use
the existing safe full-fan-out fallback, because an unavailable GitHub API
must not be mistaken for an ignored change set.

## CLI bootstrap

From a checkout, `gregale github setup <slug> --repo OWNER/NAME` binds the
application, writes `.github/workflows/gregale.yml`, and leaves the existing
preview defaults in place. Add `--preview`, `--no-preview`,
`--preview-ttl-hours`, `--preview-service-policy deny|allow_marked`,
`--root-dir`, or `--ignore` to configure the project policy in the same
command. Use `--rollout safe` to generate a production
workflow with the balanced health-gated rollout (Pro/Scale only); the default
`standard` mode preserves the existing full-traffic behavior. Use `--dry-run`
to inspect the workflow without network or file changes; an existing different
workflow is never overwritten unless `--force` is supplied. Production pushes
and manual dispatches use the workflow, while pull-request previews continue to
be managed by the connected GitHub integration.

## Related

- ADR-095 (decision + schema + state machine rationale).
- `docs/runbooks/PreviewSubdomainRouting.md` — operator
  recovery for routing failures.
- `pkg/githubd` — webhook receiver (PR-A surface).
- `cmd/apid/preview_janitor.go` — the teardown cron.

## Internal service calls from a preview

A preview is one app, not a copy of your whole project, so it has no preview
copy of the services it depends on. Service names are not environment-scoped:
if a preview is allowed to call a dependency, the destination is the
**production** service and its side effects are real.

For new projects, Gregale denies that boundary by default. The proxy returns
`403 application/problem+json` with code
`preview_production_dependency_denied` before endpoint discovery or wake-up,
so the rejected call cannot consume production capacity or reach customer
code. Existing projects retain the former behaviour until you opt them into
strict isolation:

```bash
gregale github setup checkout --preview-service-policy deny
```

Use `--preview-service-policy allow_marked` only when the production dependency
is intentionally preview-safe.

In `allow_marked` mode, Gregale marks every such call with
`X-Faas-Caller-Env: preview` and
`X-Faas-Caller-Preview-Of: <production app slug>`. Both are platform-owned and
cannot be set by a workload. See [networking](networking.md) for how to use
them to skip side effects or refuse the call.
