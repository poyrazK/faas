# Deploy from a GitHub source ref

A one-shot, CI-friendly deploy that pins a build to a specific
Git ref on a GitHub repo. Designed for runners that have no
browser and no GitHub App install token of their own — the
control plane resolves everything from the account's existing
`github_installations` row.

## Prerequisites

- `FAAS_API` — control plane URL.
- `FAAS_TOKEN` — a deploy-time API key (the `deploy` scope, or
  admin).
- `gregale connect` completed once on a workstation (browser
  available) so the control plane already has the GitHub App
  install row for the account. This is the only step that needs
  a browser — see `gregale connect --help`.

No `GREGALE_INSTALL_TOKEN_*` env vars are required. That env var
is for the `gregale scan --repo` local-fs decomposition path;
the source-ref deploy runs server-side.

## Worked example: pin to a SHA in CI

```bash
FAAS_API=https://api.faas.example \
FAAS_TOKEN=$FAAS_TOKEN \
gregale deploy --repo onebox-faas/hello --ref $(git rev-parse HEAD)
```

This is the canonical CI shape: a runner reads `HEAD` from the
local checkout and posts a one-shot deploy to the control
plane. The control plane resolves the durable install row,
mints an installation token, fetches the codeload archive for
the SHA, spools it, validates the tarball shape, enqueues a
build, and returns the build/deployment ids.

Output:

```
Deployed hello from onebox-faas/hello@<sha> (build <build_id>, deployment <deployment_id>)
```

## Worked example: pin to a branch in dev

```bash
gregale deploy --repo onebox-faas/hello --ref main
```

Branches and tags are resolved server-side to a 40-char SHA
before the codeload fetch starts, so a `main` ref that moves
between CI runs still produces an immutable SHA-pinned build
row.

## Failure modes

| Server response | What it means | What to do |
|---|---|---|
| `409 source_ref_unavailable` | Transient githubd or codeload blip. Server sets `Retry-After: 30`. | Back off and retry; the CLI surfaces the hint on stderr. |
| `404 github_install_not_found` | The account has no `github_installations` row. | Run `gregale connect` on a workstation once, then re-run CI. |
| `413 source_too_large` | Repo tarball exceeds the per-plan `SourceTarballMaxMB` cap (Free/Hobby 100 MB, Pro/Scale 250 MB). | Trim history (`git gc`), use a sparse checkout, or upgrade plan. |
| `400 invalid_ref` | `--ref` is not a branch, tag, or 7+/40-char SHA. | Pin to a SHA or a real branch / tag. |
| `429 plan_limit_*` | Per-plan concurrency / RAM cap reached. | Wait for a slot, or upgrade. |

CI retries of the same `gregale deploy` line mint a fresh
`Idempotency-Key` on every invocation, so each retry produces
a distinct build row. If your CI needs a true dedupe (same key
across retries folds to one row), set `Idempotency-Key` on the
SDK call directly — `Client.DeployFromSourceRef` accepts the
underlying HTTP shape.

## What it is NOT

- **Not a webhook bind.** For push-event auto-deploy use
  `gregale connect` and let the GitHub App's push events fire
  the build. The `--repo --ref` shape is one-shot.
- **Not a git deploy-key fetch.** The server uses the GitHub App
  install token (ADR-012, ADR-020); the control plane never
  sees the customer's PAT. The install token is scoped to a
  single `StreamSourceRef` RPC and discarded before the response
  is returned.

## Wire contract

- `POST /v1/apps/{slug}/deployments/source-ref`
- Body: `{"repo": "OWNER/NAME", "ref": "<branch|tag|sha>", "format": "tarball"}`
- Auth chain: `authLimited → requireMFA → requireScope(ScopesDeployWriteSurface) → idempotent → handler`
- SDK binding: `pkg/api.Client.DeployFromSourceRef` (Go) /
  `DeploymentsService.createDeploymentFromSourceRef` (Node).

## GitHub Actions

For teams that want explicit-CI deploys (workflow run, not push
listener), the first-party `poyrazK/faas/.github/actions/deploy`
action wraps this same endpoint. The action is a composite that
vendors the `gregale` CLI per release, so a workflow pin (`@v1`)
is deterministic and a bundled `cli-version` output surfaces the
exact version for drift detection.

### Generate a starter workflow

From the repo where you want the workflow, run:

```sh
gregale deploy --github --name my-app > .github/workflows/deploy.yml
```

The CLI emits a copy-paste workflow body to stdout. When run
inside an Actions runner (the `GITHUB_REPOSITORY` +
`GITHUB_SHA` env vars are set), the snippet hard-codes
`repo` and `ref` to those values; run from a local checkout
and the snippet emits the `${{ github.repository }}` /
`${{ github.sha }}` expressions so the same file is portable
across repos.

### What goes in the snippet

- `api-key: ${{ secrets.GREGALE_API_KEY }}` — never a literal.
  Provision a deploy-scoped API key in the Gregale dashboard
  and add it to the workflow's environment secrets.
- `api-base: https://api.faas.example` — substitute your
  control-plane host. Hobby/Pro/Scale customers each have a
  different host. The snippet's `api-base` placeholder is a
  string the customer is expected to edit.
- `app: my-app` — the slug from `gregale connect`. The snippet
  generator picks the slug from `--name` / cwd.
- `wait: "false"` — the generated workflow queues the deployment and
  continues, so a slow build does not hold the GitHub runner. Set it to
  `"true"` when the job must block until the app is live.
- `checks: write` — lets the Action publish a **Gregale deployment** Check
  Run linking to the control-plane record. The deployment still works without
  this permission; the link remains available through the Action output.

### Failure modes (Action-specific)

The action reuses the same `Failure modes` table above. The
action additionally:

- Redacts `gh*_`, `Bearer …`, and `FAAS_TOKEN=…` substrings from
  any `::error` annotation it emits.
- Surfaces the RFC 7807 `Code` + `Detail` as a single
  `::error file=action.yml,line=1::code=<code> — <detail>` line.
- Writes the new `deployment_id`, `status`, `url`, and
  `check-run-id`, and `cli-version` to `$GITHUB_OUTPUT` so downstream steps can
  chain off them.
- Appends a GitHub Step Summary with the queued/live status and deployment
  link. The default is asynchronous (`status=queued`); `wait: "true"`
  changes the terminal status to `live` or a failure state.
- The asynchronous Check Run is neutral (request accepted, deployment still
  running) rather than an indefinitely pending check; synchronous runs update
  it to the final result.

### Authentication

The Action uses GitHub OIDC by default. Grant `permissions: id-token: write`;
the job JWT is exchanged through `/v1/auth/oidc/exchange` for a five-minute
deploy bearer. The optional `api-key` input remains available for installations
that have not configured an OIDC subject binding yet.

### What's not automated by the Action
- **PR-preview environments.** Each deploy is a fresh
  deployment id; the action does not create or tear down
  preview URLs.
- **A redirect to the webhook push-to-deploy path.** The
  action is a complement to the push listener; both stamp
  `DeploymentKind = "github"` and customers pick the one
  that matches their CI shape.

See ADR-093 for the design rationale and the explicit
non-goals.

## Webhook secrets (push-to-deploy)

The push-to-deploy loop is wired end-to-end. GitHub App webhook
deliveries are signed with the App's single webhook secret, so both
`gatewayd-internal` and `githubd` must receive the same
`FAAS_GITHUB_WEBHOOK_SECRET`. `githubd` verifies the signature before
persisting the delivery to its durable inbox and acknowledging GitHub;
the worker then routes `push` and `pull_request` events asynchronously.

The older installation-scoped secret API remains only as a compatibility
fallback for non-GitHub senders that provide an explicit installation
header. It is not the normal GitHub App delivery path.

## See also

- `docs/adr/092-headless-source-ref-deploy.md` — design rationale.
- `docs/runbooks/GithubWebhookSecretRotation.md` — operator rotation flow.
- `docs/cli-setup.md` — shell completion + man page install.
