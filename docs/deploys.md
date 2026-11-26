# Deploys

The normal deploy path is source-first and wait-by-default:

```bash
gregale deploy
gregale deploy --path services/api
gregale deploy --tarball build/source.tar.gz
gregale deploy --image ghcr.io/acme/api@sha256:...
```

## What gets uploaded

By default `gregale deploy` uploads the committed `HEAD` of the selected
directory when the repository has an `origin` remote, and the files on disk
otherwise (`--source=worktree` includes uncommitted changes). Before upload:

- **Excluded by default:** `.git`, `node_modules`, `vendor`, `__pycache__`,
  `.venv`, `dist`, `.next`, `target`, `coverage`, `.cache`, `.DS_Store`,
  `Thumbs.db` and `*.pyc`, at any depth. A Go `vendor/` directory with
  `vendor/modules.txt` ships, because it is source. When the app builds with
  a Dockerfile, `dist`, `.next`, `target`, `coverage` and `.cache` ship too
  (Dockerfiles commonly `COPY target/*.jar` or `COPY dist/`), and
  `.dockerignore` applies the way `docker build` reads it.
- **`.gregaleignore`** at the source root uses `.gitignore` syntax. Later lines
  win, so `!dist/` re-includes a directory the defaults exclude. `.git` can
  never be included.
- **Symlinks** inside the source directory are uploaded as the file or
  directory they point to. A link that leaves the directory, dangles or
  contains itself stops the deploy with the link's name; exclude it in
  `.gregaleignore` or copy its target in.
- **Submodules** that are checked out are uploaded at the commit the
  repository records. A submodule that is not checked out is uploaded empty
  with a warning; run `git submodule update --init --recursive`.
- **Git LFS** files are uploaded with their real content when the objects are
  downloaded locally. Otherwise the pointer files are uploaded with a
  warning; run `git lfs pull`.
- **`.env` files** are scanned locally and credential-shaped values are
  redacted before upload (`--secret-scan=off` disables this). Use
  `--secrets-file` or `gregale env set` for real secrets.
- **Credential files in a local-files upload.** When the deploy uploads the
  files on disk (no Git repository, no `origin` remote, or
  `--source=worktree`), files that usually hold credentials stay on your
  machine: `.env` and `.env.*`, `.npmrc`, `.pypirc`, `.netrc`, `.pgpass`,
  `*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.pem` files holding a private key,
  `id_rsa*`/`id_ed25519*` private keys, `credentials.json`,
  `.aws-credentials`, `.aws/credentials`, Google service-account and OAuth
  client keys and similar files. The deploy names each file it left out on
  stderr and in the `--json` receipt's `warnings` (`.env.example`,
  `.env.sample`, `.env.template`, `*.pub` and certificate-only `*.pem` are
  uploaded). Set runtime values with `gregale env push` or `gregale env set`;
  to upload one of these files anyway, add a negated rule such as `!.npmrc`
  to `.gregaleignore`. A secret found in a file that stays local neither
  warns nor fails `--secret-scan=strict`.

### Inspect the payload before uploading

```bash
gregale deploy --dry-run --explain-files
gregale deploy --dry-run --explain-files --worktree --json
gregale deploy --dry-run --explain-files --path services/api --source=head
```

This local inspection needs no login and makes no API calls. It uses the deploy
packers and reports included paths, byte sizes, excluded paths and the ignore
file and line responsible for a match. Sizes come from the finished archive,
including generated Go build files and secret-scan replacements. Credential-like
filenames are marked; file contents, secret values and ignore-rule text are not
printed. `--secrets-file PATH` excludes that file without importing its values
as remote configuration.

The default source selection is the same as deploy. HEAD reports describe Git's
normalized archive, so uncommitted, untracked and export-ignored files are absent.
An excluded directory represents its pruned subtree. For a declared workspace
member, the report includes the repository context and the selected source root.
For a functions project, select one function with `--path`; use `--project` to
inspect a project root archive. Image, tarball, remote repository and template
inputs are not supported by file inspection.

JSON includes `source_mode`, `commit` for HEAD, `file_count`, `included_bytes`,
`compressed_bytes` and `files`. Each file records its logical `path`, included
`archive_path`, `included`, `bytes`, `reason` and any `rule_file`/`rule_line`.
Account upload limits, remote configuration and runtime readiness are unchecked;
`account_limit_checked` is false. Ordinary `--dry-run` without `--explain-files`
continues to compare authenticated remote state.

### Limits

| Limit | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| Upload (packed archive) | 100 MB | 100 MB | 250 MB | 250 MB |
| Expanded source | 2 GiB | 2 GiB | 2 GiB | 2 GiB |
| Files and directories | 10,000 | 10,000 | 10,000 | 10,000 |
| App layer after build | 256 MB | 512 MB | 1 GB | 2 GB |
| Build | 2 vCPU, 2048 MB, 15 min | same | same | same |

A deploy over a limit fails before upload with the limit and the observed
size. Large source uploads resume after a dropped connection.

### Accept-time checks

Every source deploy, from the CLI, the API, a GitHub push or a project, is
checked before a build starts:

- **Credentials.** A known credential format (cloud, payment, VCS and AI
  provider keys, private keys, Gregale deploy tokens) refuses the deploy with
  the file and line. Heuristic high-entropy values and test-mode keys do not
  block; they are listed on the deployment
  (`gregale deployment ID --show-secret-scan`). Subresource Integrity digests
  are never flagged. To keep a deliberately committed value, such as a test
  fixture or a documented example key, add `gregale:allow-secret` to its line
  in a comment.
- **Stateless contract.** Apps lose local writes when they park, so a
  database file under a top-level `data/` or `db/` directory (SQLite, DuckDB,
  Postgres, MySQL, MongoDB or Redis data) refuses the deploy, as does a
  Dockerfile `VOLUME` or filesystem mount. Migrations, schemas and seed files
  in those directories are fine. Keep data in managed Postgres or object
  storage.

### Dockerfile builds

A `Dockerfile` at the source root is built with BuildKit. To use another
Dockerfile or pass build options, declare them in `gregale.yaml`:

```yaml
build:
  dockerfile: docker/Dockerfile.prod   # relative to the source root
  target: runtime                      # a FROM … AS stage
  args:
    NODE_ENV: production
  secrets:
    - id: npmrc          # RUN --mount=type=secret,id=npmrc
      secret: NPM_TOKEN  # an app secret
```

Up to 64 arguments of at most 4 KiB each and 16 build secrets are accepted.
Secret values never reach the image or the build log. A `HEALTHCHECK` command
in the Dockerfile gates readiness: the deployment serves only after it passes,
and an instance whose check keeps failing restarts. Use `HEALTHCHECK NONE` to
opt out.

### Retries and retention

`gregale release retry` rebuilds from the uploaded source for 24 hours after
the upload; after that, deploy again from your source. A build waiting in the
queue for 6 hours, or a deployment making no progress for 3 hours, fails
with a stable error code.

When a build finishes while no compute node has room for the app's first
boot, the deployment waits in `snapshot_prepare` for up to 5 minutes and is
retried each minute; only then does it fail with `capacity_unavailable`.
A release command waits the same way: while no node has room for its task
environment, the task returns to the queue and is placed again every 20
seconds, for up to 5 minutes from the first refusal, and only then fails the
deployment with `release_infrastructure_failed`. One-off commands such as
`gregale app exec`, and command crons, do not wait; they fail at once with
`capacity_unavailable`.

## Waiting and resuming

While a build runs, its log gets one `build step:` line for each new BuildKit
step, such as `build step: [build 3/5] RUN mvn package`. `gregale deploy`
prints them as they arrive. A project deploy shows each building service's
newest step next to its stage (`orders: image_build [build 3/5] RUN mvn
package (2m10s)`), and `--json` writes a `service_build_step` event with
`service`, `deployment_id`, `step` and `elapsed_s`. A step's own output stays
out of the live view; the end of the build output follows when the build
finishes, and `gregale logs <app> --deployment <id>` shows the whole log.

Interrupted single-app deploys save a local session. Run
`gregale release resume --app my-api`, or `gregale release resume SESSION_ID --json`,
to reconnect to the same accepted deployment and finish pending lifecycle/scaling
settings without local source. Resume checks saved app identity and conflicting
settings before writing. See [saved deploy recovery](cli/deploy.md#recover-an-interrupted-deployment).

A release can be live while its cron configuration is still incomplete. Inspect
an API, Git, or CLI deployment with `gregale deployment <deployment-id>` or
`gregale deployment v42 --app my-api --json`. Deployment details include
`configuration.crons.status`, a stable `reason` code, an explanation and recovery
hint. Status is `pending`, `activated`, `conflicted`, `discarded`, or `not_managed`
when no deferred activation is recorded. Current schedules stay active while
builds or partial rollouts are in progress.

Pending cron changes activate automatically when the release is ready and owns
all production traffic. A conflict reports operator edits, changed quotas, or
cron identity changes. A discarded intent reports failed, canceled or
superseded work. Inspection and saved-session resume do not submit another build
or overwrite schedules. Inspect the stated reason and current crons before
starting a new deployment attempt to resolve a terminal conflict.

In JSON, `configuration.complete` covers server-managed cron activation.
The deploy receipt's `configuration_complete` also covers pending saved CLI
lifecycle and scaling settings. An `activated` result records that the intent
was applied; a later deployment can subsequently change schedules.

Use `--no-wait` when a CI job only needs the queued deployment ID. Use
`--timeout 900` to bound a wait, and `--idempotency-key KEY` when a retry must
represent the same logical deploy. Archive and full-commit-SHA deploys without
secrets derive a key from the source and flags, so repeating an identical deploy
returns the deployment already in progress or live. Branches, tags, short SHAs,
and secret bundles use a fresh key per invocation; transport retries reuse it.
A local wait timeout or interruption preserves accepted deployment configuration;
resume with `gregale release wait <deployment-id>`. The server bounds every
deployment on its own: a build still waiting in the builder queue after 6 hours
fails with `deployment_queue_timeout`, and a deployment with no build running
that makes no progress for 3 hours fails with `deployment_stalled` (a running
build is bounded by the build timeout). Deploy again after either. Submission errors report
accepted, rejected, or unknown acceptance. Accepted and unknown outcomes retain
staged triggers, profiling, and tracing; only definite rejection restores prior
settings. Inspect deployment history and reuse the printed retry key when
acceptance is unknown. A deployment that failed, was
cancelled, or was superseded is never replayed: re-running the same deploy
starts a new one.
`--reason`, `--tag`, and `--deployed-by` annotate deployment history.

Every successful wait ends with readiness plus a platform-side smoke request;
a queued build is not reported as live. The final output includes the app URL,
a release summary against the previous deployment, and a copy-paste rollback
command when a previous release is available:

```
✓ Deployed. https://my-app.gregale.app
Release summary:
  Changes since d0:
    commit_sha         "old" -> "new"
  Rollback: gregale rollback my-app --to d0
```

With `--json`, the receipt keeps the existing deployment/provenance fields and
adds `release_summary` with `previous_deployment_id`, `changes`,
`rollback_target_id`, and `rollback_command`. A queued `--no-wait` deploy has
no release summary yet because the new release is not live.

## Deploy from GitHub

Connect GitHub once, bind an app to a repository, and every push to the app's
production branch deploys it. Gregale reports each deployment as a `gregale`
Check Run on the pushed commit.

1. **Connect GitHub.** In the dashboard, open **Account → Connect GitHub**, or
   run `gregale github connect`. GitHub asks you to authorize Gregale and, if
   the Gregale GitHub App is not installed on your account or organization
   yet, to install it and choose its repositories. It does not matter whether
   you signed in to Gregale with GitHub, Google or a password.
2. **Bind an app.** In the dashboard, **New app** creates an app bound to a
   repository; an existing app's page has **Bind a repository**. From the
   CLI:

   ```bash
   gregale github repos my-api                       # repositories the installation can see
   gregale github link my-api --repo acme/my-api      # production branch: the repository's default
   gregale github link my-api --repo acme/my-api --branch release
   gregale github status my-api
   ```

3. **Push.** A push to the production branch builds and deploys the app. Put
   `[skip deploy]` in the commit message to skip one push. Several apps can be
   bound to one repository; each push deploys every one bound to the pushed
   branch, and, for a project, the project's apps whose source changed. An
   app bound on its own deploys beside a project bound to the same
   repository.

Without `--branch`, the production branch is the repository's default branch
on GitHub at the time you bind.

**Pull-request previews.** A pull request into a bound branch gets a preview
of every app bound to that branch, at `pr-<number>-<app>`. Closing the pull
request tears them down. Turn an app's previews off with `gregale github setup
my-api --no-preview` or `PATCH /v1/apps/my-api/github/deployment-policy`
with `{"preview_enabled": false}`.

A new commit on the pull request rebuilds only the preview services whose
files changed since the previous commit, by the same rule as production
pushes: a file outside every service's directory rebuilds them all, and
ignored paths are left out. The other services keep serving the build they
already have, and take only the deploys the rebuilt services need. A service
whose preview is not live, and every service when the previous commit's
preview failed or the pull request was just opened or reopened, rebuilds.

**Release tags.** A tag push deploys the apps bound to the branch whose head
the tag points at; a tag on an older commit deploys nothing. A project's tags
deploy its production branch. Annotated tags (`git tag -a`) and lightweight
tags both deploy the commit they point at.

**Several installations.** If your GitHub user can connect more than one
installation (your personal account and an organization, say), Connect GitHub
asks which one. Connect another later the same way. With several connected,
name one with `--installation-id` (`gregale github status` lists them).

**Who can connect.** A personal installation can be connected by its owner;
an organization's installation by an admin of the organization. Each
installation belongs to one Gregale account.

If GitHub shows only other people's personal installations, Connect GitHub
continues with installing Gregale on your own account. Connection failures in
Settings → Integrations explain the reason and next step. **Install on GitHub**
starts a fresh installation request when you need to choose another account;
the return to Gregale remains verified.

**Deploy from GitHub Actions instead.** `gregale github setup my-api --repo
acme/my-api` binds the app, writes a deploy workflow, and hands production
deploys to the workflow (`production_trigger: actions`), so a push deploys
once. Preview lifetime, bot and draft pull requests, a root directory,
ignored paths and branch mappings are project settings: they need an app that
belongs to a project.

**Deploy only after CI passes.** A push the GitHub App deploys does not wait
for your tests. To deploy only commits your CI passed, name your CI
workflow (its `name:` field): `gregale github setup my-api --repo acme/my-api
--wait-for CI`. Setup checks that a workflow of that name in
`.github/workflows` runs on pushes to the deploy branches and refuses
otherwise. The workflow then deploys a branch push once that
workflow's run for the commit succeeds; a failed run, a pull request run and
a run from a fork deploy nothing. If runs finish out of order, a commit that
is no longer the branch head is skipped, so production never goes back to an
older commit. Release tags and manual runs still deploy directly.

**Renames.** Renaming or transferring the repository, or renaming the user or
organization, keeps the binding.

**When it does not deploy.** `gregale github status my-api` shows the binding,
its health and the recent GitHub events that reached the app, with the reason
an event deployed nothing (for example, production deploys come from your
GitHub Actions workflow, or nothing deploys that branch). `gregale github
sync my-api` rechecks access after you change the installation's
repositories on GitHub.

| Error | Meaning |
|---|---|
| `github_org_admin_required` | Only an admin of the organization can connect its installation. |
| `github_installation_owner_required` | A personal installation can be connected only by its owner. |
| `github_permission_pending` | An organization owner has not accepted a permission the Gregale GitHub App asks for. |
| `github_account_conflict` | The installation is connected to another Gregale account. |
| `github_installation_required` | Several installations are connected; pass `--installation-id`. |
| `github_repo_not_accessible` | The installation cannot see the repository; add it to the installation on GitHub. |

## GitHub Actions

Print a workflow starter with:

```bash
gregale deploy --github
```

The starter deploys pushes to the repository's default branch. If the
Gregale GitHub App also deploys the repository's pushes, each push deploys
twice; use `gregale github setup` instead, which writes a workflow and hands
production deploys to it.

The generated workflow uses an immutable deploy Action SHA from the CLI
release, and Dependabot can update it from the accompanying `# v0` comment.
Pass `--pin-action` to resolve the current `v0` tag during generation. Connect
a repository with `gregale connect` when pushes should deploy automatically.

GitHub Actions OIDC deployments are tied to the repository connected through
the GitHub App installation. Gregale records GitHub's owner and repository IDs
when the repository is bound. If an existing connection predates this identity
check and the workflow uses GitHub's immutable subject format, rebind the
repository through the GitHub installation before deploying. A transfer to a
different owner also requires a rebind; a repository rename keeps the same
identity.

A repository connected through a project works the same way. Gregale asks
GitHub for the repository's ID through the project's installation each time
the workflow signs in, so the installation must still have access to the
repository.

### Deploy tokens

Other CI systems deploy with a deploy token: a credential bound to one app.
It deploys that app and applies the configuration a deploy carries (app
settings, crons, queue bindings, routes, edge rules, inbound webhooks). It
cannot delete or rename the app, add log drains, mirrors or outbound webhooks,
mint consumer keys, AI access tokens or issue ingest tokens, or read the app's
debug requests, crash snapshots or forks; those answer `403` and need an API
key. The OIDC bearer the deploy Action uses has the same limits. Browser sessions with MFA enabled need verification from the last
five minutes when minting or rotating one. CLI requests use their scoped
bearer credential. The token is shown once;
store it as `GREGALE_TOKEN` in the CI secret store.

A deploy token can also manage its app's edge rules, but only for that app's
own hosts: a rule's `match_host` must be `@app`, the app's
`<slug>.gregale.app` host or one of its custom domains. The same goes for
`routes` and `async_routes` in a `gregale.yaml` it deploys. A rule that covers
other hosts (`*`, a wildcard, another app's host), or a route that sends
another app's host to this app, needs an account API key, because edge rules
apply to every host they cover across your apps. A route rule written with a
deploy token may serve only the token's app: routing its hosts to another app
needs an account API key too. For the same reason a deploy token cannot
change, delete or roll back over a rule an account credential wrote for wider
hosts, even when the rule is listed under its app: that answers 403 naming the
rule's host, and an account API key makes the change.

```bash
gregale app deploy-token create --app my-api --label ci   # default expiry: 90 days
gregale app deploy-token list --app my-api
gregale app deploy-token rotate <token-id> --app my-api   # the old token stops working
gregale app deploy-token delete <token-id> --app my-api
```

With the token in `GREGALE_TOKEN`, `gregale deploy --name my-api … --wait` reads
the app, deploys it and follows the deployment to the end. The token reaches
no other app, and it cannot read build logs: the wait reports the
deployment's status and failure reason instead. Use a key with `apps:read`
and `gregale logs my-api --deployment <id>` to read the build. A key created
with only `deploy:write` and the deploy Action's OIDC credential work the
same way, for any app in the account.

These credentials deploy a `gregale.yaml` with workflows, companions,
extensions, crons and queue bindings; Gregale checks the plan limits when it
creates the deployment. A function-set deploy with crons needs a
`deploy:write` key or the OIDC credential, since a deploy token reaches only
its app's routes. Attaching databases or binding buckets grants the app a
credential, so a manifest with a `databases` block needs `postgres:manage`
and one with a `buckets` block needs `storage:manage`. The deploy stops before
it uploads and names the missing scope.

## Revisions

Every deploy creates an immutable revision, numbered per app and shown as
`v41`, `v42`, `v43`. A revision is never rewritten — a redeploy supersedes it
and creates the next one — so it is a stable handle you can keep in a runbook
or an incident channel.

Deploys of one environment go live in the order you start them, not the order
they finish. If you start another deploy before an earlier one goes live, the
earlier one never goes live: it is superseded when it reaches the cutover,
whether or not the newer deploy succeeds.

When a revision goes live, the previous one stops taking new requests at once.
Its instances that are still answering requests keep running for up to
30 seconds so those requests can finish, then park. An instance with nothing
in flight parks immediately. The draining instances use the one extra
instance a rollout may run beside the app's limit, so a one-instance app keeps
its old instance's requests while the new revision starts. Only an app that
is already past that, its limit plus the extra instance, parks the old
instances at once so the new revision can start.

Use a revision anywhere a deployment ID is accepted:

```bash
gregale rollback my-api --to v41
gregale release traffic set --app my-api --release v42 --percent 10
```

`gregale release traffic status my-api` lists the live revisions and their weights.
While a canary rolls out it also prints the canary's current step and the
`gregale deployment advance --expected-step` command for it.

Setting one revision's weight hands the rest to the other live revisions, in
proportion to their current weights, or evenly when all of them are at 0%. Two
kinds of revision stay at 0% and never take that remainder: a canary its
rollout aborted, and a revision that has not served yet (one staged with
`--no-traffic`). They receive traffic only when you name them. A change that
would leave only such revisions to take the remainder fails with
`traffic_percent_sum_invalid` (409).

Every revision in a split runs its own instance, and an app runs at most its
`max_concurrency` plus one instance during a rollout. A split can therefore
send traffic to at most `max_concurrency` + 1 revisions: with the default
`max_concurrency` of 1, that is two. A change that would send traffic to more
fails with `traffic_split_release_limit` (422), which reports the limit and
the requested count. Take a revision out of the split first, or raise the
limit with `gregale app my-api --max-concurrency N` (up to your plan's
concurrency).

Revision numbers are per app, so `v41` of one app is unrelated to `v41` of
another. Numbers may skip values: preview deploys and retries consume them
too.

## Cancel

`gregale release cancel <id>` stops a deployment that is still pending,
building, imaging or snapshotting. A live deployment cannot be cancelled
(`deployment_cancel_live_forbidden`); roll back to an earlier one with
`gregale deploys rollback` instead.

`gregale release hide <id>` hides a finished deployment from lists. It does
not stop anything, so a deployment that is still in progress is refused with
`deployment_hide_in_flight`: cancel it first, then hide it.

## Deployment aliases

A deployment alias gives one exact revision a stable hostname of its own:

```bash
gregale release alias set --app my-api --name candidate --release v44
```

An alias can point only at a live or in-progress deployment, and that
deployment's build is kept while the alias exists. Each app can have 5
aliases on Free, 10 on Hobby, 25 on Pro and 50 on Scale. Setting a new alias
above the limit fails with `plan_limit_deployment_aliases` (403), which reports
the limit and the current count; re-pointing an existing alias always works,
so delete one (`gregale release alias delete`) to make room.

## More on this topic

- [Safe releases](releases.md) — Safe changes, database releases and rollbacks, release gates, and the release page.
- [Release approvals, freezes and schedules](release-controls.md) — Require approvals, freeze deploys, and schedule releases for a chosen time.
- [Progressive rollouts](rollouts.md) — Dark deployments, canary stages, safe-by-default rollouts and sticky revisions.
- [Atomic project releases](project-releases.md) — Release every service of a project together, with readiness checks and schema contracts.
- [Environment promotion](environment-promotion.md) — Promote a tested artifact and its configuration from one environment to the next.
