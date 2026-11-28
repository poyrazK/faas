# ADR-20261113121342: Branch-backed PR previews

Status: Accepted (owner decisions 2026-11-13)

Amends: [ADR-095](095-pr-preview-environments.md) (PR previews),
[ADR-20261031193635](20261031193635-environment-branches.md) (environment branches, which
named this as needing its own ADR).

## Context

A PR preview today (ADR-095) is a separate `pr-{N}-{slug}` app per previewed workload, with
its own `pr-preview` values. It copies none of production's configuration, secrets,
databases or buckets, so a preview of a change that reads data shows an empty or seeded
world.

An environment branch copies a whole project environment lazily: configuration and secrets
in one transaction, the parent's builds published unprimed, Neon PostgreSQL branches as of
the branch point, overlay buckets, branch-scoped service DNS, and a stable URL per workload
that survives a reset (2026-11-12 amendment). Branches are made only by hand.

## Decision

A project can choose branch-backed PR previews. Owner decisions:

1. **Per-project opt-in, behind a flag.** `github_deploy_policies.preview_mode` is `app`
   (default, today's previews) or `branch`. Setting `branch` needs the runtime flag
   `environment_branch_pr_previews_enabled` (default off) and environment branches
   enabled. Projects that do not opt in are unchanged. Apps bound without a project keep
   app previews.
2. **Parent is production.** A PR's branch is `pr-{N}`, branched from `production`.
3. **A push deploys into the branch and keeps its data.** Each open, reopen, ready or
   synchronize event builds the PR head of the workloads the PR previews into scope
   `pr-{N}`. The branch is not reset; `gregale environment branch <project> pr-{N} --reset`
   makes it again from production, and the stable URLs keep working.
4. **At the branch limit the PR falls back to an app preview.** The check run and PR
   comment say why, with the limit and the observed count. So does any other reason
   branch mode cannot serve the PR (flag off, an older apid, a branch named `pr-{N}` the
   user made by hand, a non-production parent the user removed).

### Ownership

apid owns branches (customer intent). githubd asks for one over the existing apid bridge
(`pkg/apid/apidserver/githubd_bridge.go`), which embeds `UnimplementedGithubdServer`, so an
older apid answers `Unimplemented` and githubd falls back to an app preview:

- `EnsurePullRequestBranch(account, project, pr, expires_at)` creates `pr-{N}` from
  production if it is missing, or moves the expiry of the PR's own branch to `expires_at`
  (capped by the branch's longest lifetime: 30 days, 7 with database copies). It answers
  `ready` with the branch's stable workload URLs, or a fallback reason.
- `ClosePullRequestBranch(account, project, pr, expires_at)` moves the PR's branch expiry
  to the close grace (`PRPreviewClosedGrace`, 24 h), like a closed app preview; the branch
  sweeper deletes it. A reopen moves it back out.

The branch row records `pull_request_number`. Only a branch with the PR's number is the
PR's; githubd never adopts, extends or closes a branch the user made, even one named
`pr-{N}`.

### Builds

githubd stages each previewed workload's PR head as for an app preview and calls
`EnqueueBuild` on the **production** app with `deployment_scope = pr-{N}` and the new
field `environment_branch = true`. apid then:

- checks that the scope is a live branch of the app's project made for this PR;
- builds it as a deployment of kind `preview` in that scope, so it queues behind
  production builds, deploys no workflows or crons, and counts against the PR preview
  share of the deploy rate, like an app preview build;
- runs with the branch's copied configuration and does **not** apply the PR's
  `gregale.yaml` app settings, which are stored on the production app;
- skips the preview-app steps (`pr-preview` value sync, preview databases), which would
  otherwise act on the production app.

githubd's check sync reports such a build (kind `preview` on an app that is not a preview
app, in a non-default scope) with the branch's stable URL.

A PR whose project switches back to `app` mode keeps its branch until it expires.

### Slice 2 (2026-11-14)

- **Unchanged workloads keep their build.** On a push, githubd keeps an app's build when
  its live deployment in `pr-{N}` is a build of this PR at an earlier commit and the files
  changed since that commit lie outside the app's directory, by the production push's path
  filter (a shared file rebuilds every app). This needs no preview set: the branch's own
  deployments record each app's commit. An opened or reopened PR, a deployment copied from
  production, or a compare GitHub cannot answer exactly rebuilds everything, and so does a
  push that would keep every app, so the head always gets a deployment to report on.
- **Branches end with the project's previews.** The transactions that mark a project's app
  previews stale (previews turned off, the bound app unbound or moved to another
  repository, the installation revoked, the repository removed) also set the expiry of the
  project's pull request branches to now. The branch sweeper deletes them within a minute,
  and the orphan reconcilers remove their data copies. Branches made by hand keep their
  expiry.

### Amendment 2026-11-26: a pull request's data follows the previews setting

Owner decision: a branch preview gets what each production database's `previews` setting
(ADR-20261102123523) gives an app preview, not always a copy. A copy of production data
reaches code under review only where someone who may restore the database chose it, the
rule ADR-20261102123523 set for app previews. Branches made by hand are unchanged: they copy
every database.

For each production database the PR branch's parent binds:

| `previews` | The `pr-{N}` branch gets |
|---|---|
| `copy` | A point-in-time copy as of the branch point, as before: every copy at the same instant. |
| `empty` | The PR environment's empty preview database (owner: the project and the PR number), set up with `EnsurePreviewDatabase` and bound into `pr-{N}` with the production binding's environment key and access. An app preview of the same PR, after a fallback, binds the same database. It needs the `preview-databases` capability, as app previews do; without it `empty` reads as `off`. |
| `off` (default) | No database. The workload starts without the binding's environment key, as an app preview does. |

- **Waits and failures.** An empty database or binding still provisioning is in progress (no
  failed attempt). On a plan that gives no preview databases, `empty` reads as `off`. Past
  the plan's preview database allowance the branch's data waits and retries with that
  limit's message: closing a pull request frees a database.
- **Reset.** `--reset` removes the branch's bindings to its empty databases before it deletes
  the branch, and only once the branch row is gone purges each one nothing live binds (a
  binding neither ready nor provisioning is a leftover), so the new branch starts from an
  empty database again. A reset refused for child branches keeps the database and its data;
  a purge that conflicts leaves the database to the new branch and the sweep.
- **Delete and expiry.** The preview database sweep keeps a PR environment's databases while
  its project has a branch for that pull request (`pull_request_number`), and purges them
  once neither the branch nor an app preview is left.
- The restore lag (2 s after the branch point) applies only when the branch copies a
  database.

### Amendment 2026-11-28: production trial fixes

The slice 4 trial (2026-11-28) found three gaps, now closed:

- **PR builds wait for the branch's data.** A PR build into a branch whose data is still pending
  went live with no database URL. A build in a branch's scope now waits like the branch's own
  deployments: imaged holds its release command (the release sweep resumes it), schedd does
  not prime it, and the branch data reconciler primes one without a release command once the
  data is ready (`EnvironmentBranchDataReady` covers deployments in the branch's scope).
- **Bindings made after the copy.** A PR build reopens a ready branch's data when the app has a
  production binding, of a database with previews copy or empty, under a key the branch's
  scope does not bind. The reconciler's pass is idempotent and adds the binding; the build
  waits for it. App previews set up their databases on every build the same way.
- **In-progress budget.** A copy or binding still being made is checked without spending an
  attempt for 10 minutes from when it was made, not from the branch's creation. A branch whose
  copy could not start for two hours (stale usage) spent its last attempts on a database that
  was only provisioning.
- **Compose build context on GitHub builds.** A project deploy stages a Compose service's build
  context (`build: ./web`) as the archive root; a GitHub build staged the whole repository, so
  the service's Dockerfile built against the repository root (`"/app.py": not found`), on push
  and on PR builds alike. Project reconcile now records `build_compose_context` on the app, and
  the GitHub bridge narrows the staged repository to the app's RootDir when its Dockerfile is
  there. gregale.yaml, release commands and the source checks are still read from the
  repository, as before. An app reconciled earlier gets the field with its next change.

## Consequences

- A branch preview serves production's configuration and, per database, a copy of its data,
  an empty database or none (amendment 2026-11-26). Data written in the preview stays there
  across pushes and is gone when the branch is.
- A branch with database copies lives at most 7 days. A PR open longer gets a new branch on
  its next push once the old one has expired.
- Forked pull requests stay refused (ADR-095 D3): a branch holds production's secrets.
- Previews count against the plan's branch limit (Hobby 2, Pro 5, Scale 20), not the PR
  preview app allowance; past it, PRs fall back to app previews.

## Slices

| # | Scope |
|---|---|
| 1 | Policy field and flag, bridge RPCs, branch-scoped PR builds, close and reopen, PR comment and check with the branch URL, fallback. |
| 2 | Skip rebuilding workloads whose files did not change (as ADR-20261108150000 does for app previews); retire branches when previews are turned off or the repository is unbound. Built 2026-11-14 (above). |
| 3 | Data follows each database's previews setting (amendment 2026-11-26). |
| 4 | Production trial with a test repository (flag on for one account, then the PR journey); then branch mode becomes the default for projects and app previews are retired, each in its own PR. |
