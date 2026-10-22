# Deploying from the CLI

`gregale deploy` ships a directory, tarball, OCI image, or pinned
GitHub ref to an app on the control plane. The path that runs is
inferred from flags + the cwd's git state; this page captures the
non-obvious pieces (which files actually get shipped and what the
`--json` envelope looks like).

Use `gregale deploy --help` for common options and runnable examples. Add
`--all` for every flag grouped into source, resources, projects, rollout,
access, and other tasks. `gregale app --help` follows the same pattern;
`gregale app my-api scale --help` explains resource changes and saved plans.
Connection profiles go before the command (`gregale --profile staging deploy`);
RAM/CPU sizes use `--size` after it (`gregale deploy --size small`); the older
`--profile SIZE` spelling after the command still works but is deprecated.

## Your first deployment with `gregale start`

Run one command in a terminal:

```bash
gregale start
# Or select a project from another directory:
gregale start ./my-api
```

`start` accepts one optional project directory and needs no deployment flags.
It selects an unambiguous app automatically. For a healthy project with a saved
login, review the source, app, connection, detected build, environment, access,
and resources, then confirm deployment. New apps use account plan resource
defaults; existing apps keep their resource settings.

Browser login appears only when needed. Starters ask for a language (Node.js,
Python, or Go) and a local directory. Recognized workspaces offer a service picker.
Before deployment review, Start checks that the server can verify the accepted
deployment. It repeats this read immediately before creating an app, configuring
secrets, or uploading source. Unsupported installations show version/configuration
guidance; a temporary service outage asks you to retry later. These checks submit
no remote work and keep any previous saved launch. They take at most five seconds
per read and do not guarantee that capacity will remain available after review.
Start uses the selected source's nearest `.gregale/project.json` when it is linked
with `gregale link`. It verifies the project, workload membership, and any named
environment, then shows that target in the review. A project without a default
app offers a workload picker when needed. After a successful launch finishes,
Start remembers the selected app next to the source, including standalone apps.
It preserves conflicting saved defaults and keeps accepted-deployment recovery
available. A link in the original working directory
does not apply to a different source tree you select.

Without a link, the app name comes from the selected directory and the environment
is `default`. A name collision offers a choice:
create an app with another name, update the existing app, or finish. Updating a
recorded app checks its identity again before deployment; a new-app conflict does
not fall back to updating another app. Existing apps retain their access settings;
new apps use the current plan's defaults. Use `gregale deploy` and
`gregale app` when you want to customize deployment or access settings.

The review includes app slots in use and remaining, whether this launch needs a
new slot, plan RAM and concurrency defaults, and included compute usage when the
account API supplies them. It links to usage and explains idle parking. These
observations are a review aid; the server enforces current limits at deployment.

**Start uploads working files, including uncommitted changes**, with the normal
source exclusions and secret scan. Starter destinations must be empty. Declining
deployment keeps the local files and submits no deployment.

Launch progress stays compact: real deployment events show building, startup,
and readiness, with elapsed time. Full build logs remain available through
`gregale logs` or the failed-deployment recovery prompt. A completed build alone
does not mark the app ready. Older servers without stage events show a waiting
message until the deployment's final state is confirmed.

After the build and readiness checks, the session automatically sends the first
GET request. Starters use `/`; existing projects use a verified or detected health
path when available. Public requests carry no account credentials and report the
actual HTTP status and a bounded response body. A health response answered at the
gateway does not verify the app; choose **Check another GET path** and use an app
route such as `/` to continue. Private apps use an authenticated
control-plane response check pinned to the accepted app, deployment and environment.
The gateway can wake that exact deployment through the normal scheduler admission
path. The check reports the actual HTTP status and a response preview; an HTTP
401 or 403 from application-owned authentication remains a failed check.
For named environments, public checks require a live URL identifying the accepted
app and deployment. An unavailable URL stops the check.
Default-environment preview URLs must also identify the accepted app and deployment
before Start sends a request. An unavailable default preview can use the app URL;
the response must still supply accepted-deployment evidence.
The success screen shows the exact public URL that answered, including the tested
health path or deployment preview, response time, and total session time. Private
app URLs are labeled separately because an owner-authorized check does not verify
public ingress or customer authentication. Account credentials stay on the control
plane; checks do not follow redirects or submit durable invocation work. Private
checks have a 20-second bound and a 4096-byte UTF-8 response preview, with truncation
shown explicitly. Response previews show up to eight lines. Older servers without
response-check support fail visibly; Start never substitutes a default-environment
invocation. Deploy gatewayd-internal support before enabling the new API/CLI flow.
Every launch, including a first launch or an older saved session, requires evidence
identifying the accepted deployment. Serving deployment headers must match it.
A successful public response without deployment
identity is followed by an owner-authorized check of the accepted deployment.
That result is labeled as an owner check and does not claim public URL verification.
If an older server cannot provide this check, the launch remains unverified and
keeps its accepted ID for recovery; an HTTP 200 alone cannot mark it successful.
Public denials, redirects, edge health answers, and mismatched deployment headers
remain failures; they do not trigger this check.
If server support disappears while continuing an accepted launch, Start prints
the saved deployment ID and instructions to check the installation and CLI versions.
It does not offer app logs or alternate paths for an incompatible server. A
temporary verification-service outage offers retry or finish while keeping the
accepted ID. Retrying after recovery submits no replacement deployment.
If the request fails, the session explains the failure and stays open. Retry the
GET check on the accepted deployment, view recent app logs, choose another
app-relative GET path, or finish. This also helps when a root route redirects or
requires authentication. The starter greeting check keeps its original path and
expected message. Request retries do not submit a
deployment or change access settings. Logs use a short, bounded preview with
credential redaction; unavailable logs do not prevent a retry. Finishing or closing
an unresolved check keeps its failure exit code. Ctrl-C returns 130.

After the first successful check, existing projects offer **Finish** or
**I've made changes — review and deploy again**. Finish is the default. Edit your
project in another terminal or editor, then choose the review option. Start shows
added, modified, and deleted file names since the previous source review, including
uncommitted files, with normal source exclusions and the selected secrets file
excluded. File contents stay out of the change summary. Workspace projects review
the complete build context, including shared files. After resuming a session,
the earlier source comparison is unavailable, so the review lists current files.

Each update repeats source and required-configuration checks and asks for a fresh
deployment confirmation against the recorded account, app, and environment. Edits
after the preview require another review. A new response check reuses the previous
successful GET path and requires evidence identifying the accepted deployment;
an older deployment or a response without deployment identity cannot verify the
update. Request failures keep the usual retry, logs, and alternate-path options.
After a successful update, you can edit and deploy again or finish. Closing or
declining before confirmation submits no update.
Function-set manifests use the regular `gregale deploy` flow; a guided launch
keeps its single app target.

Finishing prints commands for deploying changes, reading logs, opening
the app, and deleting it. Commands retain the connection, source, and deployment
scope; deletion still asks for confirmation. Built-in starters offer an optional next step with two answers: enter a
greeting, then approve a combined file-edit and deployment preview. Press Enter at
the greeting prompt to finish. Declining or closing before confirmation keeps the
original files and submits no deployment. After approval, the local edit remains
available if deployment fails. The session requests GET `/` to verify the new JSON
`message`; retries keep the same expected greeting and path. An old greeting cannot
complete the walkthrough, and serving deployment headers are checked when present.

### Continue an interrupted launch

An accepted deployment ID is saved before waiting. Ctrl-C stops the local session
and leaves the remote deployment running. Repeat `gregale start [path]` with the same
original project directory and choose **Continue** to reconnect without submitting another
deployment. The saved session also remembers source selected from another directory.
For an update, the newly accepted ID is saved immediately while the previous
successful response check's deployment ID remains recorded until the new check
succeeds. A failed or interrupted update shows an inspection command for that
previous deployment. Continue reconnects to the accepted update without submitting
another deployment.
Recovery checks the API, account, project, environment, app, and deployment identity and does not require
local source files to still exist. A new deployment still needs those files.

A saved launch under another account, API, or linked target offers **Start a new launch** or
**Finish and keep the saved launch**. A separate launch uses its own local record;
the previous accepted deployment remains recoverable when you switch back.
Neither option resumes a deployment using a different account, API, or linked target.
Changing a source link after review stops submission. Retrying a saved deployment
uses its recorded target; restore its source link before submitting another revision.
Follow-up deploy commands explicitly preserve the selected environment, and logs
are filtered to the accepted deployment. Named environments show the tested URL
or a deployment inspection command.

Local records use mode `0600` in `$XDG_STATE_HOME/gregale/start` or the Gregale
user-config directory. They contain recovery metadata: paths, project, environment, app and account IDs,
deployment IDs, revisions, status, timestamps, and health paths without query strings.
Local source comparisons and file hashes stay in memory. Tokens, secret values,
secrets-file paths, greetings, response bodies, and request
query values are omitted. Concurrent sessions in the same directory are refused.

If the server accepts source but its reply is lost, a saved pending submission
lets Start recover the exact deployment through its upload ID. This check happens
before another launch can be offered, and an update keeps its previous accepted
and verified IDs until the new ID is recovered. A fully uploaded source can
continue its original commit. Recovery of a committed upload uses reads only;
it does not need the source files or the normal upload cache, and does not repeat
app creation, secrets or source upload. The normal readiness and response checks
still have to succeed.

The pending record adds only a retry key, transport, upload ID and byte count.
An older CLI refuses a pending record; after recovery the record remains readable
by older versions. Missing discovery records, service outages or changed target
identities keep the launch pending. You can finish and keep its record. You can
also cancel an unfinished upload before reviewing source again; a committed
deployment is recovered rather than cancelled. On older servers using legacy
multipart upload, an uncertain reply needs manual deployment-history inspection.
Start preserves that record and does not automatically submit another deployment.

The deployment wait uses the normal 1200-second default. Timeout returns exit code
3; interruption returns 130. For a custom timeout or automation, use `gregale deploy`.
`start` requires a terminal and rejects JSON output.

### Help only when something needs attention

Source blockers keep you in the session. Fix files in your editor and recheck,
choose another directory, or finish. Simple Node loopback listeners can receive a
reviewed fix to `0.0.0.0`; the session asks before writing. Guided edits preserve
permissions and refuse changed files, symlinks, and paths outside the source tree.
Complex source issues need a manual fix. Healthy source checks stay brief.

Required runtime keys come from source references and the selected source's
`.gregale/required-secrets.json`, when present. Optional source references remain
optional unless explicitly declared required. Start reconciles these names with
the selected app's environment and sealed-secret metadata in the reviewed scope and the
validated secrets file you select. Explicit required-secret declarations need a
sealed secret; ordinary environment metadata cannot satisfy them. Metadata proves
key presence, not value validity, delivery, or application adoption. Failed metadata
reads remain errors rather than being treated as missing or valid configuration.

A local `.env` or `.gregale/env.json` alone does not deliver runtime configuration.
Missing keys offer a local `KEY=VALUE` secrets-file prompt; selected required values
must be nonempty. The deployment review lists the keys that will be sealed before
upload. The selected file is excluded from source and values are sealed through
the normal deployment path. Its values and path stay out of saved session metadata.
Selecting another source directory clears the secrets-file selection. A failed
deployment offers logs and a reviewed retry, including secrets-file configuration
when needed.

## Source-root semantics

App and environment defaults come from the selected source directory's nearest
saved context. `--name` and `--environment` override those defaults. Selecting
`--path` does not replace a saved app name with the directory name or inherit an
unrelated environment from the caller's directory.

When cwd is inside a git repository with an `origin` remote, `gregale
deploy` without a source selector still ships the
**committed tree at HEAD from the repo root**, preserving the original
zero-config behavior. A nested working directory does not implicitly
change the build root.

Use `--path` when one service in a monorepo should be deployed as its
own app:

```bash
cd monorepo
gregale deploy --path packages/api --name api
```

`--path` is resolved relative to the current directory. For a
self-contained directory, it archives `HEAD:<path>` and makes the
selected directory the uploaded archive root, so its `package.json`,
`go.mod`, or `Dockerfile` is detected normally. When the selected
directory is a deployable member of a recognized workspace manifest
(`package.json`, `pnpm-workspace.yaml`, `go.work`, and the other
`reposcan` workspace forms), Gregale uploads the repository tree as the
BuildKit context and records `source_root=<path>`. The builder runs in
that nested directory while retaining workspace lockfiles, shared
packages, and root-level build configuration.

Workspace context uploads still use the normal source exclusions,
secret scan, and source-size cap. `source_root` is validated against the
archive before the deployment is queued. It never requires removing
`.git`. The default remains reproducible and excludes uncommitted
changes.

For scripts and CI, choose the local source explicitly:

```bash
gregale deploy --source=head       # require a Git commit; upload committed HEAD
gregale deploy --source=worktree   # upload local files, including changes
```

`--source=head` works even when the Git repository has no `origin` remote.
It fails before upload if the directory is not a Git repository or has no
commit. `--source=auto` is the default: it uses committed `HEAD` when
`origin` exists and local files otherwise. Pinning a mode prevents a change
to the repository's remote configuration from changing which files deploy.
The source option accepts local directories, including `--path`; it cannot
be combined with an image, tarball, GitHub ref, template, `--github`,
`--create-only`, or the legacy `--worktree` spelling.

Use `--worktree` to explicitly deploy local files from the selected
directory, including uncommitted and untracked files:

```bash
gregale deploy --path packages/api --worktree --name api
```

`--worktree` without `--path` applies the same behavior to the current
directory. It remains available for existing scripts and is equivalent to
`--source=worktree`. Both modes keep the repository's `commit_sha` in the JSON
receipt when Git metadata is available; `dirty: true` indicates that
the repository had local changes at deploy time.

Deploy waits for readiness by default. Use `--no-wait` for queue-only CI steps,
or bound the wait explicitly with `--timeout` (seconds):

```bash
gregale deploy --timeout 1200
gregale deploy --no-wait
```

The default wait is 1200 seconds (20 minutes), covering the server's 15-minute
cold-build budget plus post-build scanning and snapshot preparation. A timed-out
wait returns a non-zero exit code but retains the accepted deployment ID in
`--json` output, along with an exact `resume_command`; resume it with
`gregale deploy resume <session-id> --timeout ...` to also finish pending settings.

Before changing remote state or uploading source, human-readable deploys print
one deployment plan containing the selected source, runtime resolution,
resource behavior, environment, and release policy. Local-source, archive,
template, and image plans include locally inferred start, listener, and health
details when available. A `--repo` plan names the exact repository and ref,
marks runtime detection as remote-after-checkout, and makes clear that an
existing app's resources are preserved. In a dirty Git checkout the plan states
whether local changes are included. The default deploy ships committed `HEAD`;
use `--worktree` when the plan reports that local changes are excluded. JSON
output remains a single machine-readable deployment receipt and does not
include this human preflight block. A dirty checkout also prints an exclusion
warning to stderr, including with `--json`, so a script's stdout stays parseable.

## Understand a preview

`deploy --preview` (also spelled `--plan`) and `deploy --dry-run` show the same connection, app,
environment, source, and local-change summary. Their verification differs:

- `--preview` performs local inference without authentication. It does not check
  the app's current remote configuration or guarantee build success.
- `--dry-run` compares supported changes against remote state. It does not run
  the build or verify readiness, and state can change before deployment.

The single-app diff engine cannot compare named project environments. A saved
or explicit named environment produces an explanation; use a project preview,
or `--environment=` to intentionally compare the default scope. It never silently
switches a named environment to the default scope.

Structured previews retain their existing fields and add summary metadata.
Scale plans and secret-update previews use the same human summary and explain
their verification limits. Secret previews compare key metadata without reading
remote secret values or verifying their delivery.

## Safe production rollouts

A production release of an app that already serves traffic uses the
adaptive health-gated canary and first-wake 5xx rollback by default
([ADR-1446](../adr/1446-safe-releases-by-default.md),
[ADR-1321](../adr/1321-evidence-paced-default-rollouts.md)); see
[Safe by default](../deploys.md#safe-by-default). To also wait for the
rollout to reach 100% before the command returns, use the safe path:

```bash
gregale deploy --rollout safe
```

`--rollout safe` (formerly `--safe`) selects Gregale's adaptive 1% → 10% → 50% → 100% rollout, where
each stage advances as soon as the evidence is clean after a minute, enables
first-wake 5xx auto-rollback, and waits for the rollout to reach 100% traffic
before returning success. The server's smoke verification, configured health
gates, and rollback behavior remain the source of truth. Safe rollouts are
available on every plan, and the 5xx rollback protection cannot be disabled on
a safe deploy.

Preview the safe-release plan before uploading:

```bash
gregale deploy --rollout safe --dry-run
gregale deploy --rollout safe --dry-run --json | jq '.diff.safe_release'
```

The preview shows the next rollout step, the actionable alert-gate status,
and the previous deployment that would be the rollback target. If no enabled
`rollback` or `demote` alert rule exists, the preview warns that the rollout
has no actionable health gate. Create one from the CLI with `--action`, for
example:

```bash
gregale alerts add --app APP_ID --name release-errors \
  --metric error_rate_pct --comparison gt --threshold 5 \
  --window-spec 5m --action rollback \
  --webhook-url https://example.com/hooks/gregale \
  --webhook-secret-stdin
```

The always-on circuit breaker gates every canary even without an alert rule;
an alert rule adds your own signal on top. Outside a terminal and without `--rollout safe`, a canary deploy
(defaulted or explicit) returns once the candidate is live; inspect or wait
for the full rollout with:

```bash
gregale deployment wait <deployment-id> --rollout
gregale deployment wait <deployment-id> --rollout --progress
```

With `--progress`, the CLI prints one line for each lifecycle or rollout
transition, such as `1% traffic · step 1/4` followed by `10% traffic · step
2/4`. JSON output remains a single deployment record. If the wait times out,
the JSON receipt includes the exact resume command.

Archive and full-commit-SHA deploys derive a stable retry key from the app,
source and deploy intent. Branches, tags, short SHAs, and deploys with a secrets
bundle use a fresh key per invocation; transport retries keep that key. This
lets a new invocation deploy a moved ref or changed secrets. Pass
`--idempotency-key KEY` to reuse one logical key across separate invocations. The CLI scopes the
logical key per transport and resumable-upload chunk before sending it to the
API, so unrelated mutations cannot replay one another.

## Optional hosting overrides

Zero-config inference is the default. If a repository uses a non-standard
entrypoint or readiness route, add a small `hosting` block to the
`gregale.yaml` (or `gregale.yml`) next to the deployed source:

```yaml
hosting:
  start: "npm run serve"
  port: 8787
  health: /ready
```

Each field is optional. `start` replaces the inferred process command,
`port` selects the listen port, and `health` selects the HTTP readiness path.
The CLI validates the block before creating the app; the same values are
applied to the server-side profile captured from the exact uploaded source.
Omitting the block leaves the normal framework profile unchanged.

## Declarative scaling

The same manifest can declare the app's autoscaling policy. The block is
applied after the deployment is accepted, and is idempotent on repeat deploys:

```yaml
schema_version: 1
scaling:
  min_instances: 0
  max_instances: 5
  targets: # rps, cpu, concurrent_requests, or queue_depth
    - metric: concurrent_requests
      value: 2
    - metric: cpu
      value: 70
  scale_out_cooldown_s: 5
  scale_in_cooldown_s: 60
  concurrency_overflow: queue # queue or drop
  max_queue_depth: 32 # warm-saturation waiters; 0 uses the plan default
  max_queue_wait_ms: 2000 # warm-saturation wait; 0 uses the plan default
```

`min_instances` and `max_instances` use the platform's plan limits; `0`
means scale to zero (and a zero `max_instances` means the plan maximum).
Cooldowns default to 5 seconds for scale-out and 60 seconds for scale-in when
omitted. Each entry in `targets` says how much load one instance should carry;
when several are declared the platform provisions for whichever demands the
most instances (see [Scaling policy](../scaling-policy.md)). The singular
`target:` form is still accepted as a one-element list.
The API remains the final authority for plan gates and workload-class
compatibility. A project (`--project`) deploy currently rejects the top-level
block because scaling is app-scoped; configure each workload separately after
project apply. Source-ref (`--repo`) deploys read and apply the block
server-side from the immutable archive before enqueueing the deployment. A local
single-app deploy reads the block from the uploaded source.

`concurrency_overflow: drop` returns HTTP 429 immediately when the app's
concurrency boundary is saturated; `queue` preserves bounded FIFO waiting.
The warm queue's effective plan defaults are shown by `gregale app APP`, and
are independent of the longer `wake_max_queue_*` cold-wake limits.

## Declarative worker lifecycle

Use `lifecycle` to make a long-running container a first-class worker. Workers
do not need an HTTP listener, are exempt from idle reaping, and can be paired
with queue bindings and `queue_depth` scaling:

```yaml
lifecycle:
  execution_mode: worker
  restart_policy: always
  startup_deadline_s: 30
  max_retries: 5
  request_timeout_s: 20
```

The equivalent one-off override is `gregale deploy --execution-mode worker`.

Queue-driven pools scale out on the tick that observes the backlog. Scale-in
waits for demand to stay low for 60 seconds: a pool shrinks only to the highest
replica count its queue called for during that window, so a queue that drains
and refills does not stop and re-boot workers.

A worker consumes a queue either way: a push binding delivers each message to a
worker that listens for deliveries on its port, and a pull binding lets a
worker with no HTTP listener lease messages itself
([pull leases](../queue-pull-leases.md)). A worker
holding unexpired leases is kept when the pool scales in. On the stop signal,
stop leasing and finish or nack in-flight messages within
`stop_grace_period_s`; an unacked lease is redelivered after its visibility
timeout.

Lifecycle settings are applied idempotently after the app is created; plan and
mode compatibility checks remain server-authoritative. Project deploys reject
single-app lifecycle flags.

For a decomposed monorepo deploy (one CLI invocation, N apps), opt into
project apply with `--project`. The project slug defaults to `--name`, the
selected `--path`, the tarball basename, or the current directory (in that
order); use `--project-slug` when the slug must be stable across CI runners:

```bash
gregale deploy --path . --project --yes
gregale deploy --tarball ./shop.tar.gz --project-slug shop --yes
```

Run `gregale scan --path . --project-slug shop` first when you want a
read-only plan. `--only`, `--exclude`, `--show-affected`, and `--dry-run`
continue to use the same project planner. A direct `--path` deploy without
`--project` is still one app per invocation, with the selected workspace
member as its working directory. Source-ref (`--repo`) deployments remain
single-app only.

## Monorepo / nested-project detection

When the cwd contains **monorepo workspace markers** in a nested
subdir (e.g. `apps/web/package.json`, `apps/services/api/package.json`)
and there is no explicit `--path` selection, the CLI prints a hint and
exits with a "no deployable source here" error rather than guessing:

```
note: detected nested project marker(s) at apps/web/services/api/package.json
hint: this looks like a monorepo subdir; run `gregale scan --path .` to
      decompose into per-app plans and deploy each one.
```

The detection walks depth 2 from the cwd (so `apps/web/package.json`
and `apps/services/api/package.json` both trigger it). An explicit
`--path` is the operator intent that enables the workspace-context
behavior above. Depth 4+ remains intentionally out of scope for the
cwd hint — a `pkg/billing/internal/lib/` marker is too deep for the CLI
to act on without explicit operator intent. See
`detectNestedMarkerHint` at `cmd/gregale/pack.go:691`.

## `--json` receipt shape

`gregale deploy --json` emits a single JSON document with the
`DeploymentResponse` shape promoted to top-level (via embedding)
plus provenance and local recovery fields:

| Field            | Type   | Source                                                          |
|------------------|--------|-----------------------------------------------------------------|
| `id`             | string | `api.DeploymentResponse.ID` (server-issued)                      |
| `app_id`         | string | `api.DeploymentResponse.AppID`                                  |
| `status`         | string | `api.DeploymentResponse.Status` — `"pending"` at deploy time    |
| (all other `DeploymentResponse` fields) | — | see [`pkg/api`](../../pkg/api)                                 |
| `app_url`        | string | `deployedAppURL(slug)` — `https://<slug>.<FAAS_APPS_DOMAIN>` (default `gregale.dev`). Slug comes from `--name` / cwd-derived name (CLI input), NOT from `app_id` on the response — the wire's `app_id` is the 32-char hex primary key and the gateway routes on slug. |
| `commit_sha`     | string | `git rev-parse HEAD^{commit}` from the zero-config branch; empty on image / source-ref / non-git fallback paths |
| `dirty`          | bool   | `git status --porcelain` is non-empty; omitempty so a clean repo renders no key |
| `source_sha256`  | string | lower-case hex sha256 of the tarball bytes just shipped; empty on image and source-ref (server pulls) paths |
| `session_id` | string | local saved deploy session, scoped to the selected profile, endpoint and credential |
| `resume_command` | string | command to reconnect to this session and complete pending settings |
| `configuration_complete` | bool | whether every saved lifecycle/scaling settings step has completed |

The receipt is consumed by CI / GitHub Actions tooling that needs to
pin a deploy to a specific upstream artifact. Parse with any JSON
decoder that accepts extra top-level keys: existing SDK clients that
unmarshal into `api.DeploymentResponse` keep working — the extra
fields are silently dropped.

For the source-ref CI path, commit pinning is captured server-side
rather than in the receipt (the CLI never sees the tarball bytes).
See [`docs/source-ref.md`](../source-ref.md) for the
`--repo OWNER/NAME --ref $SHA` shape and the install-token trust
boundary.

## GitHub release tags

After an app is connected to GitHub, a push to the configured production
branch still deploys as before. A newly-created SemVer release tag also
deploys through GitHub's normal `push` webhook: use the conventional
`vMAJOR.MINOR.PATCH` shape (for example `v1.4.0` or `v1.4.0-rc.1`). githubd
uses the tag's immutable `after` SHA and applies the repository's production
binding.

Release tags are a one-way promotion boundary. Moving or force-updating an
existing tag is ignored, so a release cannot silently change underneath a
customer's deployment history; publish a new version instead. Tags that do
not satisfy SemVer, tag deletion webhooks, and malformed tag deliveries are
also ignored before source fetch or build enqueue. The repository default
branch is the initial lookup key, with the project's configured production
branch as the authoritative fallback.

## Reproducibility note

Three flavors of "what was deployed", pinned differently:

- **Zero-config (`gregale deploy` from a git repo)**: pinned at
  `commit_sha` (HEAD) + the committed tree of HEAD. Without `--path`,
  that tree is the repository root. With `--path`, a self-contained
  source uses the selected `HEAD:<path>` tree; a recognized workspace
  member uses the repository HEAD tree plus its `source_root`. Re-runs
  of the same SHA are byte-identical assuming the selected tree/context
  is unchanged.
- **Working-tree zero-config (`--worktree`)**: pinned by the shipped
  `source_sha256`; `commit_sha` and `dirty` remain useful provenance,
  but local edits and untracked files are intentionally included.
- **Tarball (`gregale deploy --tarball foo.tar.gz`)**: pinned at
  `source_sha256` (the bytes shipped). Re-runs of the same SHA
  are byte-identical. No `commit_sha` because no git detection ran.
- **Image (`gregale deploy --image registry.x/app@sha256:...`)**:
  pinned at the OCI digest in `--image`. `dep.ImageDigest` on the
  response carries the same value.
- **Source-ref (`gregale deploy --repo OWNER/NAME --ref SHA`)**:
  pinned at the GitHub ref. SHA-pinned refs (`--ref
  $(git rev-parse HEAD)`) are byte-identical upstream; branch refs
  are not. See [`docs/source-ref.md`](../source-ref.md).

## Startup readiness probes

Source deployments infer an HTTP readiness path by default. For a pure HTTP/2
gRPC app, select the standard gRPC health service explicitly:

```sh
gregale deploy --app --app-protocol grpc --healthcheck-grpc
# Check a named service instead of overall server health:
gregale deploy --app --app-protocol grpc --healthcheck-grpc --healthcheck-grpc-service audit.Echo
# Override inferred HTTP readiness:
gregale deploy --app --healthcheck-path /readyz
```

Exactly one HTTP path or gRPC selector is allowed. The selected service must
implement `grpc.health.v1.Health/Check` and return `SERVING` before startup
readiness succeeds. Omitting both selectors preserves source inference.
These flags work with local directories, archives, resumable uploads,
`--repo`/`--ref`, and image deployments. They configure startup admission;
steady-state readiness and liveness remain separate API settings.
They require a single-app deployment and cannot be combined with project
selection, preview (`--plan`, `--diff`, `--dry-run`), `--github`, or `--create-only`.

## Recover an interrupted deployment

Single-app deployments save recovery metadata before staging profiling or tracing
and before submission. Reconnect to the latest local attempt for an app, or select
the exact session from a receipt:

```bash
gregale deploy resume --app my-api
gregale deploy resume SESSION_ID --json --timeout 1200
```

Resume waits for the same deployment's readiness and the original safe rollout,
then completes saved lifecycle/scaling settings. It works from any directory
without the original source or manifest. It never creates an app, uploads source,
submits another build, or promotes a dark deployment. A conflicting change to a
pending field or a newer app deployment stops settings recovery. Fields already
at the desired value count as complete, including after a lost update reply.
Pending settings writes include server-enforced preconditions for the exact app,
newest live deployment, and every targeted field. The server checks and updates
inside one database transaction, preserving edits made after resume's read.
Conflicts return `409 deploy_settings_conflict`, identify changed field names,
and leave the session pending. Unrelated edits are preserved when rebuilding a
lifecycle update from the current app row. Older servers that do not support
these preconditions reject the update; resume never falls back to an unchecked
write.

Each attempt gets its own record; `--app` selects the most recently created one,
including an attempt that failed before acceptance. Use its explicit session ID
to recover an earlier accepted deployment. Records are private local files under
`$XDG_STATE_HOME/gregale/deploys` (or the user config directory), retained until
removed by the user. They contain no credentials, secret values, environment
variables, or source bytes; a policy journal saves only typed profiling and tracing
settings and recovery metadata. Keep the original profile, endpoint and credential;
credential rotation selects a different local scope. Project apply continues to
use its project recovery workflow.

Before deployment submission, selected profiling and tracing policies are recorded
in a private journal with their previous and intended values. The server stages
them atomically with a revision and ownership guard. A hard process kill or lost
staging reply can be recovered with the same `deploy resume` command, without
source files or the manifest. Recovery restores only policies still owned by that
attempt. A concurrent policy edit, including an edit away and back or an explicit
same-value policy PATCH, stops obsolete cleanup. Unrelated manifest edits survive
cleanup. Cleanup that arrives before a delayed stage fences that stage out.

The journal marks `submission_started` immediately before a deployment-creating
HTTP send, including resumable upload commit. If saving the marker fails, no
request is sent. Upload start and chunk transfer do not mark submission. An old
uncertain upload commit is preserved conservatively. Accepted or uncertain
submission keeps policies; a definitively rejected or never-submitted attempt can
finish cleanup. A missing deployment receipt never authorizes rollback of an
uncertain submission.

A cleanup-only resume returns success after recovering profiling and tracing and
creates zero deployments. Its JSON receipt contains `session_id`, `app_id`,
`app_slug`, `submission_outcome` (`not_submitted` or `rejected`),
`policy_cleanup_complete: true`, `deployment_created: false`, and `resume_command`.
Rerun the original deploy to submit after cleanup. A conflict leaves the journal
pending and the current configuration intact. Old servers cannot stage journaled
policies; there is no unchecked fallback. Old saved sessions without a policy
journal keep their previous behavior.

Manifest-managed HTTP cron triggers also use a saved journal and atomic server
staging. Resume restores their previous scheduling set after a pre-submission
crash without creating another build. Explicit `triggers: []` clears HTTP crons;
omitted triggers and `--no-triggers` leave them unmanaged. Command crons remain
untouched and count toward app/account quotas. Whole-set replacement works at
the exact quota, and a quota failure leaves all schedules unchanged.

Concurrent HTTP cron edits, including same-value updates and edits away and back,
leave cleanup pending. Scheduler firing and suspension bookkeeping are preserved.
Recovery restores scheduling configuration; it cannot undo already dispatched
work or deleted run history and related resources. Cleanup JSON also includes
`cron_cleanup_complete: true` when cron recovery is complete or unmanaged.
Accepted or uncertain submissions preserve the staged cron set. Old servers fail
before staging; there is no unguarded CRUD fallback.

The journals cover profiling, tracing and HTTP crons for single-app CLI staging.
Bindings, secrets, project apply and server-side source-ref manifest staging
retain their existing recovery behavior.

For unknown acceptance, resume reads durable acceptance evidence without sending
another deployment. Image, multipart and source-ref submissions save an opaque
attempt ID before sending the request. Resume looks up that attempt's server
receipt and reconnects to the original deployment, including a deployment that
has since failed or been superseded. Source files and the original manifest are
not needed. The receipt is stored atomically with deployment creation and retained
until the deployment or app is purged.

Resumable uploads use their saved upload receipt: a matching committed session
establishes the deployment ID. An open, expired, missing or unavailable receipt
leaves acceptance uncertain. Older saved attempts without a submission attempt ID,
and servers that do not support receipt lookup, still require inspection of
deployment history for image, multipart or source-ref acceptance. A missing
receipt never triggers resubmission or proves rejection.
Resume cannot finish a source upload that stopped before acceptance; rerun the
original deploy with the same source and retry intent as described below.

Submission failures now include recovery details: the stage (`upload`,
`submission`, or `deployment`), app, any confirmed deployment ID, an inspection
command, and the logical idempotency key when that transport uses one.
In JSON mode, these appear in the stderr Problem object's `recovery` field,
including `submission_outcome`: `accepted`, `rejected`, or `unknown`.
Its `next_command` identifies the recommended action: resume observation of an
accepted deployment, or inspect history when acceptance is unknown. Failed
deployment receipts recommend logs for that exact deployment instead of waiting
again. `inspect --errors` includes the same recovery data alongside its existing
deployment fields, including when the server has not recorded a failure code.

A confirmed deployment ID or committed upload preserves staged triggers,
profiling, and tracing even if fetching deployment status fails. A definite
rejection restores the previous settings. Unknown acceptance keeps the settings
and reports a nonzero exit; it does not schedule automatic rollback. After an
uncertain resumable commit, Gregale checks the upload session once, for at most
two seconds, including after Ctrl-C. A matching committed receipt recovers the
deployment ID while preserving the original error or interruption exit.

If no deployment ID was confirmed, inspect the app's history before retrying:

```bash
gregale deployments --app my-api
```

A submission error does not prove the server rejected the request. Retry the
original command with the printed `retry_flag`, keeping the source, deploy
options, account, API endpoint, and selected profile unchanged. The flag uses
the same logical key that the original request used; the CLI scopes wire keys
per operation. Replay protection is limited by the server's retention window.
Use a new logical key for an intentional new deployment. Upload-stage failures
retain their resumable session on interruption, request timeout, or exhausted
transient retries. Rerunning with unchanged source and intent finds the saved
session even though Gregale creates a new temporary archive, asks the server for
its acknowledged offset, and sends only the remaining bytes. If a commit response
was lost, the retry retrieves the original accepted deployment instead of
submitting another build. A developer source-sync failure does not advertise a retry key
because that transport does not use this deploy key.

Recovery requires the same credential, endpoint, app, source bytes, options, and
logical key. A secrets bundle uses a fresh key by default, so reuse the printed
`retry_flag` to resume it. Expired or cancelled sessions restart the upload when
commit was not attempted or was definitely rejected. Once a commit is uncertain,
an open, missing, or expired session cannot prove rejection: Gregale retains the
checkpoint and avoids starting a replacement deployment. Inspect deployment
history before explicitly choosing a new intent or changing retained settings.
The server's existing upload TTL is 24 hours. Local checkpoint metadata is kept
for at most seven days, with a maximum of 256 records. Inspect it with
`gregale upload-cache list`, or remove it with `gregale upload-cache cleanup`.
Cleanup only removes local metadata; abandoned server bytes expire separately.
Older checkpoint formats cannot be resumed after upgrading. Credentials and
source bytes are never stored in the checkpoint directory. Canary/traffic
deploys and older-server multipart fallbacks still use a single-request upload.

When a deployment ID is known, inspect or resume that deployment instead of
submitting another one:

```bash
gregale deployment get DEPLOYMENT_ID
gregale deployment wait DEPLOYMENT_ID --timeout 1200
gregale deployment wait DEPLOYMENT_ID --rollout --timeout 1200
```

Generated recovery commands preserve the selected named connection through
`gregale --profile NAME`. Keep any API and credential environment overrides the
same as the original operation.

For JSON deploy waits and resumes, the stdout receipt includes `session_id`,
`configuration_complete`, and `wait_stage` (`deployment`, `rollout`, or
`configuration`) with `recovery` when completion stops. Waited success is emitted
after pending settings complete. A deadline sets `timed_out`
and retains exit `3`; cancellation sets `interrupted` and exits `130`.
The stderr Problem contains matching inspection and resume guidance. A terminal
failed deployment or aborted rollout exits `1` and identifies its stage in the
receipt. Stopping a local wait leaves server processing running.

### Secrets and accepted deployments

`--secrets-file PATH` keeps the selected file out of automatically packed source,
including committed HEAD, even when secret scanning is off or `.gregaleignore`
tries to reinclude it. Keep secrets out of any archive you supply with `--tarball`.

For projects, apply carries the bundle separately and seals it for every selected
workload in the requested environment before it queues any build. This requires
`secrets:write` or `admin` in addition to deploy permission. A secret configuration
failure queues no builds; app rows and any secrets already written remain available
for retry. The bundle is not a database transaction across workloads.

Accepted or uncertain submission preserves staged triggers, profiling, and tracing configuration.
A readiness timeout or interruption ends only the local wait and preserves that
configuration. Resume with `gregale deployment wait <deployment-id>`.

## Retry failed project workloads

```bash
gregale deploy retry SESSION_ID --path . --source=worktree --yes
gregale deploy retry SESSION_ID --tarball source.tar.gz --yes
```

Retry reads current statuses from a saved project deploy and rebuilds only
confirmed failed deployments, including when source is unchanged. Supply source
again; the saved session contains identities, not source or secrets. The current
source is scanned and reviewed. Healthy, pending and unconfirmed workloads are
excluded. Replaced apps or newer deployments stop the retry.

Use `--path DIR` to select a directory, or omit source flags to use the current
working directory. `--source=auto` follows normal deploy behavior: committed HEAD
when Git provenance is available, otherwise working files. `--source=head` requires
a Git commit; `--source=worktree` includes local changes. `--worktree` is an alias
for `--source=worktree`. Directory retries use normal file exclusions and secret
scanning. Tarballs cannot be combined with directory source selectors.

The new submission gets its own saved session and resume command. `--no-wait`
returns after acceptance; `--timeout SECONDS` controls waiting. Current schedules
and secrets are preserved. Configuration conflicts, cancelled deployments and
unconfirmed build publication need inspection before another apply. Deploy the
updated APID before using this command.

## Safe project source releases

```bash
gregale deploy --project --atomic --project-slug shop --path . --source=worktree --yes
```

Safe mode rebuilds the complete existing HTTP project at zero traffic and keeps
its previous release selected until every candidate completes readiness. APID
then publishes the ready graph atomically. A failed or incomplete batch leaves
production on the previous graph. The server continues after the local wait
ends; use `--no-wait` to return acceptance, or the saved resume command to reconnect.

An active production release set and revision retention on every workload are
required. The first version accepts source-only changes: membership and workload
configuration must match. It preserves secrets and triggers and refuses service
workers, images, managed resources, release commands, operation declarations,
partial selection and exclusion changes. Existing release-set routing and
retention rules apply. Candidate IDs remain available for inspection and cleanup
when a batch fails. This mode does not promise rollback of database side effects
or automatic rollback after cutover.

### Verify an atomic project release

```sh
gregale deploy --project --atomic --verify --project-slug shop --path . --yes
```

Configure GET/HEAD route-health probes for critical endpoints first. This mode
requires an existing production release graph with at least 15 minutes of
retention and a configured gateway probe sender. It supports up to five probe
routes across the project and inherits the source-only HTTP restrictions of
`--atomic`.

After cutover, Gregale probes the new and retained previous deployments. Two
healthy rounds a minute apart report `succeeded`. Repeated candidate 5xx with a
healthy baseline restore the complete previous graph. If health remains unknown
for ten minutes, Gregale also attempts rollback; missing evidence never counts
as healthy. If the latest probes show that the previous release also fails,
Gregale reports `rollback_failed` for operator action. Critical probes require 2xx responses and preserve authentication.

Verification and rollback continue on the server after disconnect. Use
`gregale deploy resume SESSION_ID` to observe the result. `rolled_back`,
`rollback_failed`, and `conflicted` return a failing exit status. A newer operator
release is preserved. JSON receipts include the probe evidence and, after
rollback, `restored_release_id`.

### Deploy code and non-secret variables together

Create `release.json` with complete variable sets for the workloads you want to
change:

```json
{"env":{"api":{"MODE":"new","LOG_LEVEL":"info"}}}
```

```sh
gregale deploy --project --atomic --verify --config-file release.json --project-slug shop --path . --yes
```

Each named workload replaces its entire plaintext variable snapshot. Use `{}`
for an empty set; omitted workloads inherit their previous release variables.
Existing non-secret project metadata is preserved. Secret values, managed-binding
shadows, resource settings and route changes are unsupported. Existing variable
quotas and a 64 KiB document limit apply. The explicit configuration file uses
its current bytes even when code is selected with `--source=head`.

Candidates receive their new variables before readiness. The old release retains
its variables until cutover, and code plus the project configuration version
switch in one transaction. Verification failure restores the old graph and
configuration together. A newer operator configuration stops automatic activation
or rollback with `conflicted`.

Once configured, a deployment uses immutable variables on later cold reads.
Subsequent atomic source deployments inherit them without needing the local file.
Use `--config-file` to update those variables; later `env set` writes cannot change
an already pinned deployment. Secrets keep their existing separate delivery path.
Cloning or synchronizing configuration from these releases is currently refused
until snapshot transfer is supported.

`deploy resume` observes the server operation even if `release.json` has been
removed. Configuration values are absent from saved recovery journals; receipts
show the request/target fingerprints and `target_version` / `restored_version`.
The mode requires the configuration migration and updated runtime readers before
production use, plus the probe and retention prerequisites of `--verify`.

### Database migrations before atomic cutover

Use `--migrate` with a verified atomic project deploy to run one declared,
backward-compatible migration before switching production traffic:

```bash
gregale deploy --project --atomic --verify --migrate --project-slug shop --path . --yes
```

Declare the command in `gregale.yaml` for a single-workload project:

```yaml
release:
  command: "npm run db:migrate"
```

Alternatively, declare one `release: npm run db:migrate` process in the root
Procfile. Multi-workload projects use the Procfile's deterministic release owner.
The mode requires an existing HTTP production release, revision retention and
configured verification probes. Add `--config-file release.json` to give the
migration and candidate the proposed non-secret variables.

`--migrate` asserts that the previous release remains compatible with the schema
changes. Use expansion migrations such as new tables or nullable columns. The
old release keeps serving while the command runs. Gregale does not inspect SQL
or reverse database changes, including when a command fails halfway through or
post-cutover verification restores the previous code and variables.

The deploy receipt includes `safe_release.release_command` with the immutable
command hash, task ID, state and exit/failure evidence. Read the task's bounded
stdout/stderr through `GET /v1/apps/{slug}/tasks/{task_id}` or the SDK's task-read
method; task reads require the existing app-task API gate. A lost result after
execution starts is reported as `uncertain` and blocks cutover. Inspect the task
and database before starting a new deploy. `gregale deploy resume <session-id>`
only reads the original attempt and never reruns its command; source files are
not needed to resume.

The first milestone supports one command and refuses conflicting pending atomic
migrations. It does not coordinate migration commands in other projects or
ordinary deployments. Enable `FAAS_RELEASE_PHASE_ENABLED=1` on imaged and
`FAAS_APP_TASK_DISPATCH=1` on schedd, update APID/CLI/SDKs and complete native-host
acceptance before production enablement. Commands fail closed when execution is
unavailable. Destructive schema changes and automatic schema rollback are outside
this contract.

### Verified push-worker handoff

Use `gregale deploy --project --atomic --verify --workers` for a project with HTTP
workloads and exactly one existing production push-queue worker. The worker must
have one enabled named-production push binding and one matching consumer.
`--config-file` and `--migrate` can be combined with this mode.

Candidate startup/configuration proof gates cutover; platform production jobs
continue through the previous graph until publication. Old workers retire within
their configured stop grace (default 30 seconds, maximum accepted 60 seconds).
`deploy resume SESSION_ID` reconnects to the durable attempt without rebuilding or
resubmitting. Rollback restores graph admission and paired variables, preserving
completed-job, external and database effects. The server gate is disabled by
default pending native acceptance; see [ADR-20261019120008](../adr/20261019120008-atomic-project-push-worker-handoff.md).
