# PR preview environments (issue #272 / ADR-095)

Every pull request against a connected GitHub repo gets an
ephemeral app for the bound workload and its declared transitive
`depends_on` workload dependencies, deployed on push and routed at
per-PR subdomains. They are torn down on PR close (or after the TTL —
whichever comes first). No CI configuration required; the integration is
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

For a GitHub-managed PR, `preview show` displays every recorded workload at
the current PR head. `preview wait` succeeds only when the whole set is live;
a live root app alone is not enough. A new push moves the command to the new
head rather than leaving it watching obsolete deployments. Use `--progress`
for per-workload transitions or `--json` for a receipt containing aggregate
readiness, member statuses, the preview URL, expiry, timeout resume command,
and the next diagnostic action. Developer and older PR previews without a
recorded workload set retain app-level behavior.

For recorded PR workload sets, `preview show` also includes each member's
production comparison and its URL, logs, metrics, and configuration links.
Artifact changes are compared only when both deployments are known; configuration
differences are reported as categories such as `runtime`, `routing`, or `policies`.
The comparison never includes secret values. The same per-workload summary is
shown on the root preview's dashboard page.

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

Before applying a PR webhook, Gregale verifies the current PR state and head
with GitHub. Delayed updates for an older head and delayed close events after
reopening are ignored. If two preview builds overlap, the older build cannot
take traffic after a newer preview deployment has been accepted. See
[ADR-312](adr/312-pr-preview-freshness.md).

## Quota

Each preview workload consumes **one slot** of the separate
`PreviewApps` allowance:

- **Free** — 1 preview slot in addition to the production app.
- **Hobby** — 2 preview slots.
- **Pro** — 5 preview slots.
- **Scale** — 20 preview slots.

Production apps keep their own plan limit. A preview with two app dependencies
consumes three slots. The root and its dependency previews reserve those slots atomically:
if the full set does not fit, no new preview rows are kept and no builds are
queued. Existing rows for the same PR are preserved on retries. The 7-day
default TTL plus the 24h
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
comments. The comment links to the root preview's dashboard details page so
reviewers can inspect the whole workload set, including per-workload production
changes and diagnostics. The Check Run remains the source of build-stage status.

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
services. All projects default to `preview_service_policy: deny`. Legacy
projects previously backfilled to `allow_marked` are migrated to `deny` and
must explicitly opt in again if production calls are intended. Previews are
enabled by default, with a 7-day TTL, no ignored paths, and the repository root.

When all changed files match ignored paths, githubd records the delivery as a
successful no-op and does not enqueue builds. Compare-API failures still use
the existing safe full-fan-out fallback, because an unavailable GitHub API
must not be mistaken for an ignored change set.

## CLI bootstrap

From a checkout, `gregale github setup <slug> --repo OWNER/NAME` binds the
application, writes `.github/workflows/gregale.yml`, selects Actions as the
single production push deploy owner, and sets preview-to-production service
calls to `deny`. The GitHub App continues to own PR previews. Add `--preview`,
`--no-preview`,
`--preview-ttl-hours`, `--preview-service-policy deny|allow_marked`,
`--root-dir`, or `--ignore` to configure the project policy in the same
command. Use `--rollout safe` to generate a production
workflow with the balanced health-gated rollout (Pro/Scale only); the default
`standard` mode preserves the existing full-traffic behavior. Use `--dry-run`
to inspect the workflow without network or file changes; an existing different
workflow is never overwritten unless `--force` is supplied. Production pushes
and manual dispatches use the workflow. Add `--deploy-branches staging=staging`
to route pushes to the registered `staging` project environment; mappings
already saved through `github bind` are reused when the flag is omitted. Passing
the flag replaces the saved mapping. The workflow listens only to the configured
production branch and mapped branches, and manual dispatch is limited to those
branches. A `default` mapping uses the app's default scope and keeps the
workflow's `production` GitHub Actions environment protections. Existing
projects that do not run setup retain webhook-owned
production deploys. To opt into production service calls from previews, pass
`--preview-service-policy allow_marked` explicitly.

The generated workflow uses the maintained `@v0` deploy Action tag by default.
Pass `--pin-action` to resolve the tag to its current commit and write that
immutable SHA into the workflow:

```bash
gregale github setup checkout --repo OWNER/NAME --pin-action
```

`--pin-action` contacts the public Gregale repository to resolve the tag, so
it cannot be combined with `--dry-run`. Pass a full commit with `--pinned-sha`
when you need a network-free preview or want to choose a specific release.

Add `--enable-action-updates` to merge a weekly `github-actions` updater into
`.github/dependabot.yml`. Existing Dependabot ecosystems and their settings
are preserved, and an existing root Actions updater is left as configured.
When `--pin-action` generates the workflow, its same-line `# v0` comment lets
Dependabot update the immutable SHA to the latest v0 release. With
`--dry-run --enable-action-updates`, setup previews both generated files.

Generated workflows serialize deployments by app and Gregale target scope.
Pushes to separate mapped environments can proceed independently, while
branches and tags targeting the same scope share a deployment queue.

## GitHub merge queues

GitHub merge queues require the workflow that reports your required CI checks
to run for `merge_group` events as well as `pull_request` events. Add the
following trigger to that CI workflow, keeping any existing `push` or manual
triggers:

```yaml
on:
  pull_request:
    branches: [main]
  merge_group:
    types: [checks_requested]
    branches: [main]
```

The merge group has its own temporary commit SHA and ref. Let
`actions/checkout` use the event's default SHA so the tests validate the
combined queued changes. A job that explicitly checks out
`github.event.pull_request.head.sha` will not test that merge group. Configure
the same required status checks for the target branch in the repository's
merge queue settings. See [GitHub's `merge_group` event reference](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#merge_group).

The workflow generated by `gregale github setup` handles branch pushes,
release tags, and manual dispatches. Keep merge queue readiness in CI: a merge
group is a candidate commit, so its checks must complete before GitHub adds it
to the target branch and triggers the normal deployment event.

## Related

- ADR-095 (decision + schema + state machine rationale).
- ADR-310 (production trigger ownership and separate preview quota).
- `docs/runbooks/PreviewSubdomainRouting.md` — operator
  recovery for routing failures.
- `pkg/githubd` — webhook receiver (PR-A surface).
- `cmd/apid/preview_janitor.go` — the teardown cron.

## Internal service calls from a preview

A project PR preview first resolves a service name inside its own account,
project, and PR. If that workload preview exists, the call stays isolated; a
preview in another PR, project, or account is never eligible.

On each PR head update, githubd scans the source and provisions only the bound
workload's transitive `depends_on` app closure, in dependency order. Enqueue
order does not itself guarantee that a dependency is live before its caller
starts. Unrelated project workloads are not copied. Retries reuse the same
preview rows; closing the PR closes the sibling rows together. Managed services
are external to this app fan-out. A newly declared dependency with no
production app gets a preview-only sibling from the PR source, with no
environment or credentials copied from the bound workload. If a production
app for that workload appears later, subsequent pushes reuse that sibling. An
existing non-deleted but inactive production dependency is still rejected.
The root and existing production dependencies also start with a clean app
environment: Gregale does not copy production environment values into PR
previews. It injects the PR's declared internal service bindings after source
scan. A new PR push clears environment values left on older preview rows.
Configure preview-specific credentials separately before relying on a preview
that needs external managed services; Gregale does not provision isolated
databases or buckets for the PR.
When a dependency preview is absent, the gateway considers the **production**
service and its side effects are real if policy permits it.

The `gregale-preview` GitHub check reports the **whole selected workload set**
for the current PR head. It stays in progress until every member has a live
preview deployment for that exact commit; a failed member fails the check and
the PR comment names the workload. Deployment notifications for older commits
or a closed PR cannot overwrite the current result. This is a readiness
report, not a startup-order guarantee: a caller can start before a dependency,
so applications should tolerate a brief unavailable dependency during boot.
If two PRs share the same commit, the fixed-name GitHub check stays in progress
until both PR environments are ready; each PR comment shows its own status.

Automation can read the same per-PR result with
`GET /v1/preview/{root-preview-slug}/environment` using a deployment-read
credential. The response includes the recorded head SHA, aggregate phase and
readiness, plus each expected workload's latest preview deployment for that
SHA. A missing or failed sibling cannot produce `ready: true`; a closed PR
reports `phase: closed` and `ready: false`. The endpoint returns 404 for
developer previews and legacy PR previews with no recorded set. The CLI's
`preview show` and `preview wait` use the recorded set when available and
fall back to app-level status for those older previews.

Gregale denies that boundary by default. The proxy returns
`403 application/problem+json` with code
`preview_production_dependency_denied` before endpoint discovery or wake-up,
so the rejected call cannot consume production capacity or reach customer
code. The migration resets existing `allow_marked` policies to `deny`; a
customer who intentionally uses preview-safe production dependencies can
opt in again:

```bash
gregale github setup checkout --preview-service-policy allow_marked
```

Use `--preview-service-policy allow_marked` only when the production dependency
is intentionally preview-safe.

Gregale marks every call made by a preview, including isolated sibling calls,
with `X-Faas-Caller-Env: preview` and
`X-Faas-Caller-Preview-Of: <production app slug>`. Both are platform-owned and
cannot be set by a workload. See [networking](networking.md) for how to use
them to skip side effects or refuse the call, and for the separate
preview-to-preview and preview-to-production counters.
