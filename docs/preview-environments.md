# PR preview environments (issue #272 / ADR-095)

Every pull request against a connected GitHub repo gets an
ephemeral app for the bound workload and its declared transitive
`depends_on` workload dependencies (every callable project service under the
`project` service policy), deployed on push and routed at
per-PR subdomains. They are torn down on PR close (or after the TTL —
whichever comes first). No CI configuration required; the integration is
triggered by the GitHub App the customer installed via
`gregale connect`.

## CLI workflow

Review captured route changes and available policy, performance, and lifecycle
test evidence with `gregale preview report PREVIEW_SLUG`. Use `--format markdown`
for a CI summary or `--json` for automation. The [route report guide](route-change-report.md)
describes evidence boundaries and the optional CI gates.

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
| **Torn down** | App row tombstoned, instances reaped, snapshot freed. The URL returns `404`, the PR's GitHub deployment turns inactive, and the status comment says the preview is gone. |

A reopened PR during the grace period bumps the row back to
**Open** and the URL starts serving again on the next push.

Before applying a PR webhook, Gregale verifies the current PR state and head
with GitHub. Delayed updates for an older head and delayed close events after
reopening are ignored. If two preview builds overlap, the older build cannot
take traffic after a newer preview deployment has been accepted. See
[ADR-312](adr/312-pr-preview-freshness.md).

## Quota

Each open pull request with a preview uses **one PR preview environment** of
the separate `PreviewApps` allowance, however many workloads it runs:

- **Free** — 1 PR preview environment in addition to the production app.
- **Hobby** — 2 PR preview environments.
- **Pro** — 5 PR preview environments.
- **Scale** — 20 PR preview environments.

One environment runs at most as many workloads as the plan can deploy
(`DeployedApps`: 1, 5, 25 and 100). Production apps keep their own plan limit.
The root and its dependency previews are reserved together: if the environment
does not fit, no new preview rows are kept, no builds are queued, and the PR's
`gregale-preview` check and status comment say which limit was reached.
Existing rows for the same PR are preserved on retries.

A closed PR's preview keeps serving through its 24-hour grace but no longer
holds an environment, so the next pull request can be previewed at once.
Reopening a PR takes an environment again. The 7-day default TTL and the
janitor's per-tick sweep keep the steady-state preview count bounded — a
customer who opens 20 PRs today will not have 20 previews live a week from now.
To free an environment immediately, close the PR or run
`gregale preview delete <preview-slug>`; destroying any workload of a PR's
preview tears down the whole environment.

## Fork PRs

PRs from forks are **refused**. On a connected repository with previews
enabled, the webhook posts a `gregale-preview` Check Run with
`conclusion: neutral` and `text: "Preview skipped for fork PR (security policy)"`.
No app row is provisioned. This is deliberate: we cannot
guarantee that untrusted code from a fork will not reach the
build VM without a per-install override. The GitHub App needs
`Deployments:write` for the deployment timeline and `Issues:write`
to maintain the single preview status comment on the PR's issue
thread; it does not need `Pull requests:write`.

## Dashboard

A preview is an app in the dashboard. Open `/dashboard/apps/{preview-slug}`
(the PR comment's **Preview details and teardown** link) to see the whole PR
environment: every workload, its status at the current PR head, and its
changes from production. The app menu's **Tear down preview** action destroys
the environment. The project page's **PR previews** panel lists the previews of
the project's apps.

## Pull request feedback

Gregale maintains one bot comment per preview PR. The comment is
updated on `opened`, `synchronize`, `reopened`, and `closed` events, as
deployments progress, and when the preview is torn down. It includes the
preview URL, current lifecycle status, commit SHA, and links to the latest
workload deployment, its logs, and the root preview's dashboard page, where
reviewers inspect the whole workload set and tear the environment down. A
hidden marker makes webhook retries and repeated synchronize events
idempotent; Gregale only ever edits its own bot comment, never a comment
someone else wrote with the marker. The Check Run remains the source of
build-stage status.

If Gregale cannot set up a preview for the PR head, the `gregale-preview`
check fails and the comment says why: the project cannot be read at that
commit, a workload depends on a service that is not in the project, the
environment exceeds the plan, the hourly deploy limit is reached, the
manifest is invalid, or the account cannot deploy (details are on the
dashboard, not on the PR). `GET /v1/preview/{slug}/environment` and
`gregale preview wait` report the same failure. Push a new commit to retry.

Each preview deployment also appears in GitHub's Deployments timeline
with a stable environment URL and deployment/log links. Every preview of a
production app reports to one GitHub environment, `preview/<app>`, rather
than one per pull request. Status updates reuse the same provider deployment
across retries, so GitHub consumers see one `queued` → `in_progress` →
`success`/`failure` lifecycle rather than a new deployment row for every
phase. When the preview is torn down, its deployment turns `inactive`.

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
gregale edge rule create --app pr-42-checkout \
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
  `journalctl -u faas-apid` for `preview janitor:` warnings.
  The janitor is non-fatal on row errors; a single bad row
  does not abort the sweep. Sweep and row failures raise
  `FaasPreviewJanitorFailing`.
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
successful no-op and does not enqueue builds. The same holds for a push to a
pull request whose preview is live: the preview keeps serving its last built
commit and the new commit's `gregale-preview` check passes with that
explanation. Compare-API failures, force pushes and truncated diffs still use
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

The generated workflow pins the deploy Action to an immutable SHA embedded in
the CLI release. Its same-line `# v0` version comment lets Dependabot keep the
pin current. Pass `--pin-action` to resolve the maintained tag at setup time
and write its current commit into the workflow:

```bash
gregale github setup checkout --repo OWNER/NAME --pin-action
```

`--pin-action` contacts the public Gregale repository to resolve the tag, so
it cannot be combined with `--dry-run`. Pass a full commit with `--pinned-sha`
when you need a network-free preview or want to choose a specific release.

Add `--enable-action-updates` to merge a weekly `github-actions` updater into
`.github/dependabot.yml`. Existing Dependabot ecosystems and their settings
are preserved, and an existing root Actions updater is left as configured.
The default immutable pin and pins refreshed with `--pin-action` both carry a
same-line `# v0` comment. With `--dry-run --enable-action-updates`, setup
previews both generated files.

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
- ADR-20261026090000 (environment allowance, environment-wide destroy, setup failures on the PR, gate sync).
- `docs/runbooks/PreviewSubdomainRouting.md` — operator
  recovery for routing failures.
- `pkg/githubd` — webhook receiver (PR-A surface).
- `cmd/apid/preview_janitor.go` — the teardown cron.

## Internal service calls from a preview

A project PR preview first resolves a service name inside its own account,
project, and PR. If that workload preview exists, the call stays isolated; a
preview in another PR, project, or account is never eligible.

On each PR head update, githubd scans the source and provisions the bound
workload's transitive `depends_on` app closure, in dependency order. When the
bound workload's service policy is `project`, every other callable workload of
the project joins it, since the preview may call any of them without declaring
it; workers, jobs and scheduled workloads join only as declared dependencies
([ADR-20261025093331](adr/20261025093331-project-config-and-whole-project-previews.md)).
Enqueue order does not itself guarantee that a dependency is live before its
caller starts. Under other policies unrelated project workloads are not copied. Retries reuse the same
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
`gregale preview create` follows the same rule: the preview gets the
production app's service binding URLs and none of its environment values.
Gregale does not provision isolated databases or buckets for the PR.

## Configuration

A preview gets configuration from two places
([ADR-20261028090000](adr/20261028090000-preview-configuration.md)):

- **The pull request's Compose values.** When the Compose file opts in with
  `x-gregale-env-import: true`, each preview imports the plain values its
  service declares at the PR head, with the same rules as a project deploy.
  Secret-looking values are skipped. An address of another project service
  becomes its Gregale address, which reaches the same PR's sibling. Every push
  imports them again from the new head.
- **The production app's `pr-preview` scope.** Values and secrets you set on the
  production app in the `pr-preview` scope are given to each of its previews
  when the preview is built or reopened:

  ```sh
  gregale env set --app shop-api --scope pr-preview STRIPE_KEY=sk_test_…
  ```

  They override the PR's Compose values. Changing a value reaches each preview
  at its next build. Removing one from `pr-preview` does not remove it from
  open previews. Managed database and storage credentials are never given to
  previews. `pr-preview` cannot be the name of a project environment.

Previews keep their production app's access and egress gates: password,
IP allowlist, signed-request and consumer-auth requirements, the egress
allowlist and the app's visibility. A gate changed on the production app
applies to its live previews at once. A service the PR adds takes the bound
app's gates and the visibility production would give it, so a service
without ports stays internal.
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

## Builds, lifetime and indexing

- **Builds.** A pull request's preview builds use at most half of the account's hourly deploy limit, so production deploys keep the other half. Builders take every other queued build before a preview build.
- **Lifetime.** Deleting a production app tears down its PR previews on the preview janitor's next pass, within five minutes.
- **Indexing.** Previews answer every request with `X-Robots-Tag: noindex, nofollow` and serve a `robots.txt` that disallows everything.
