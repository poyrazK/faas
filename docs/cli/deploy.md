# Deploying from the CLI

`gregale deploy` ships a directory, tarball, OCI image, or pinned
GitHub ref to an app on the control plane. The path that runs is
inferred from flags + the cwd's git state; this page captures the
non-obvious pieces (which files actually get shipped and what the
`--json` envelope looks like).

## Your first deployment with `gregale start`

Run one command in a terminal:

```bash
gregale start
```

`start` takes no flags or arguments. It asks what to launch, chooses sensible
defaults, and gets your first app live. For an existing project with a saved
login, the healthy path needs just two answers:

1. Deploy this directory, create a starter, or choose another directory.
2. Review the source, app name, account, access, and billing, then confirm deployment.

Browser login appears only when needed. Starters ask for a language (Node.js,
Python, or Go) and a local directory. Recognized workspaces offer a service picker.
The app name comes from the selected directory. Existing apps retain their access
settings; new apps use the current plan's defaults. Use `gregale deploy` and
`gregale app` when you want to customize deployment or access settings.

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
actual HTTP status and a bounded response body. Private apps use the authenticated
control-plane invocation API and report its result without assuming an HTTP status.
The success screen shows the exact public URL that answered, including the tested
health path or deployment preview, response time, and total session time. Private
app URLs are labeled separately because the invocation does not verify public HTTP
access. Response previews show up to eight lines and 4096 characters.
Serving deployment headers are checked against the accepted deployment when present.
If the request fails, the session explains the failure and stays open. Retry the
GET check on the accepted deployment, view recent app logs, or finish. A missing
route also offers another app-relative GET path. Request retries do not submit a
deployment or change access settings. Logs use a short, bounded preview with
credential redaction; unavailable logs do not prevent a retry. Finishing or closing
an unresolved check keeps its failure exit code. Ctrl-C returns 130.

The session ends with commands for deploying changes, reading logs, and opening
the app. Built-in starters offer one optional next step with two answers: enter a
greeting, then approve a combined file-edit and deployment preview. Press Enter at
the greeting prompt to finish. Declining or closing before confirmation keeps the
original files and submits no deployment. After approval, the local edit remains
available if deployment fails. The session requests GET `/` to verify the new JSON
`message`; retries keep the same expected greeting and path. An old greeting cannot
complete the walkthrough, and serving deployment headers are checked when present.

### Continue an interrupted launch

An accepted deployment ID is saved before waiting. Ctrl-C stops the local session
and leaves the remote deployment running. Run `gregale start` again from the same
original directory and choose **Continue** to reconnect without submitting another
deployment. The saved session also remembers source selected from another directory.
Recovery checks the API, account, app, and deployment identity and does not require
local source files to still exist. A new deployment still needs those files.

Local records use mode `0600` in `$XDG_STATE_HOME/gregale/start` or the Gregale
user-config directory. They contain recovery metadata: paths, app and account IDs,
deployment IDs, revisions, status, timestamps, and health paths without query strings.
Tokens, secret values, secrets-file paths, greetings, response bodies, and request
query values are omitted. Concurrent sessions in the same directory are refused.

The deployment wait uses the normal 1200-second default. Timeout returns exit code
3; interruption returns 130. For a custom timeout or automation, use `gregale deploy`.
`start` requires a terminal and rejects JSON output.

### Help only when something needs attention

Source blockers keep you in the session. Fix files in your editor and recheck,
choose another directory, or finish. Simple Node loopback listeners can receive a
reviewed fix to `0.0.0.0`; the session asks before writing. Guided edits preserve
permissions and refuse changed files, symlinks, and paths outside the source tree.
Complex source issues need a manual fix. Healthy source checks stay brief.

Missing environment keys offer a local `KEY=VALUE` secrets-file prompt. Only keys
present in a validated file resolve their findings. The file is excluded from the
source upload and values are sealed through the normal deployment path. Its values
and path stay out of saved session metadata. Selecting another source directory
clears the secrets-file selection. A failed deployment offers logs and a reviewed
retry, including secrets-file configuration when needed.

## Source-root semantics

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
`gregale deployment wait <deployment-id> --timeout ...`.

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

## Safe production rollouts

For a health-gated production release, use the opt-in safe path:

```bash
gregale deploy --safe
```

`--safe` selects Gregale's balanced 1% → 10% → 50% → 100% rollout, enables
first-wake 5xx auto-rollback, and waits for the rollout to reach 100% traffic
before returning success. The server's smoke verification, configured health
gates, and rollback behavior remain the source of truth. Safe rollouts require
a Pro or Scale plan, and the 5xx rollback protection cannot be disabled on a
safe deploy.

Preview the safe-release plan before uploading:

```bash
gregale deploy --safe --dry-run
gregale deploy --safe --dry-run --json | jq '.diff.safe_release'
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

The existing deploy default remains unchanged. For an explicitly configured
canary, a normal deploy returns once the candidate is live; inspect or wait
for the full rollout with:

```bash
gregale deployment wait <deployment-id> --rollout
gregale deployment wait <deployment-id> --rollout --progress
```

With `--progress`, the CLI prints one line for each lifecycle or rollout
transition, such as `1% traffic · step 1/4` followed by `10% traffic · step
2/4`. JSON output remains a single deployment record. If the wait times out,
the JSON receipt includes the exact resume command.

Every deploy also has a stable retry key derived from the app, source digest,
and deploy intent. Pass `--idempotency-key KEY` when an external CI workflow
needs to reuse one logical key across separate invocations. The CLI scopes the
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
and refills does not stop and re-boot workers. When a pool does shrink,
workers with in-flight push deliveries are kept and idle ones are stopped
first. Pull-mode consumers are invisible to the platform, so they should stop
taking new messages on the stop signal and finish within the grace period.
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
plus four provenance-only fields:

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

Submission failures now include recovery details: the stage (`upload`,
`submission`, or `deployment`), app, any confirmed deployment ID, an inspection
command, and the logical idempotency key when that transport uses one.
In JSON mode, these appear in the stderr Problem object's `recovery` field.

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
can recover saved resumable state when it is available; commit-stage failures
retain their recovery state so the next invocation can discover an accepted
deployment. A developer source-sync failure does not advertise a retry key
because that transport does not use this deploy key.

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

For JSON deploy waits, the stdout receipt includes `wait_stage` (`deployment`
or `rollout`) and `recovery` when waiting stops. A deadline sets `timed_out`
and retains exit `3`; cancellation sets `interrupted` and exits `130`.
The stderr Problem contains matching inspection and resume guidance. A terminal
failed deployment or aborted rollout exits `1` and identifies its stage in the
receipt. Stopping a local wait leaves server processing running.
