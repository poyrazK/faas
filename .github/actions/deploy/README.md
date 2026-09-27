# Gregale deploy action for GitHub Actions

Official GitHub Action for [Gregale](https://github.com/poyrazK/faas) — deploy an app from a GitHub Actions workflow using a pinned `gregale` CLI.

This action is part of the Gregale [control plane](https://github.com/poyrazK/faas) — issue #270 / ADR-093. It wraps the existing `POST /v1/apps/{slug}/deployments/source-ref` endpoint; no new server surface is required.

## Usage

```yaml
on:
  push:
    branches: [main]
    tags: ["v*"]

concurrency:
  group: gregale-${{ github.repository }}-my-app-production
  cancel-in-progress: false

jobs:
  deploy:
    runs-on: ubuntu-22.04
    environment: production
    permissions:
      contents: read
      checks: write
      id-token: write
    steps:
      - uses: poyrazK/faas/.github/actions/deploy@v0
        with:
          api-base: https://api.gregale.dev
          app: my-app
          # repo / ref default to ${{ github.repository }} / ${{ github.sha }}
          # Report the terminal deployment result to this workflow.
          wait: "true"
          # For a Pro/Scale production app, use the balanced health-gated rollout:
          # rollout: "safe"
```

## Generate a starter workflow

From the Gregale CLI inside any repo:

```sh
gregale deploy --github --name my-app > .github/workflows/deploy.yml
```

The CLI emits a copy-paste workflow file. When run inside an Actions runner (`GITHUB_REPOSITORY` + `GITHUB_SHA` env vars set), the snippet hard-codes those values; from a local checkout it emits the `${{ github.* }}` expressions so the same file is portable across repos.
For a repository connected to Gregale's GitHub App, run `gregale github setup`
or set the deployment policy to `production_trigger=actions` before using a
push workflow. The App remains responsible for PR previews.

## Inputs

| Input | Description | Required | Default |
|---|---|---|---|
| `api-key` | Optional Gregale bearer. Omit to exchange GitHub OIDC for a five-minute token. | no | — |
| `oidc-audience` | Audience requested from GitHub and sent to the Gregale exchange. | no | `gregale` |
| `api-base` | Gregale API base URL. | no | `https://api.gregale.dev` |
| `app` | App slug to deploy. | yes | — |
| `repo` | OWNER/NAME of the source GitHub repo. | no | `${{ github.repository }}` |
| `ref` | git ref — branch, tag, or 40-char SHA. | no | `${{ github.sha }}` |
| `format` | Source format passed to the source-ref endpoint. | no | `tarball` |
| `wait` | If `true`, block until the deployment is live (or fails); if `false`, queue it and return immediately. | no | `true` |
| `wait-timeout` | Maximum seconds to wait when `wait=true`. | no | `1200` |
| `rollout` | Production rollout mode: `standard` or `safe` (balanced health-gated rollout; Pro/Scale only). | no | `standard` |

## Outputs

| Output | Description |
|---|---|
| `deployment-id` | The new deployment id (32-char hex). |
| `app-slug` | Echo of the input `app` slug. |
| `status` | Observed status: `live` when waiting succeeds, `queued` when waiting is disabled, `skipped` for a superseded push, or the terminal failure/timeout status. |
| `rollout` | Selected rollout mode: `standard` or `safe`. |
| `url` | URL of the deployment record on the control-plane API (`{api-base}/v1/apps/{slug}/deployments/{id}`). |
| `check-run-id` | GitHub Check Run id containing the deployment link; empty if the workflow cannot write Checks. |
| `cli-version` | Bundled `gregale` CLI version (verifies the vendored binary). |

When `checks: write` is granted, the action creates a **Gregale deployment**
Check Run with a direct link to the control-plane deployment record. The Check
Run is updated to the terminal result. With explicit `wait: "false"`, the
Action instead creates a separate **Gregale deployment queued** Check Run;
that check only confirms admission and must not be used as a release gate.
Check publication is best-effort, so missing permission never
blocks the deployment.

On a branch push that deploys `github.sha`, the Action checks the current
GitHub branch head before submitting the deployment. A superseded run or an
old rerun exits with `status=skipped` and never queues a stale release. On a
`v*` tag push, it accepts only a new, unforced SemVer tag creation; moved,
deleted, and invalid tags exit with `status=skipped`. The deployment uses the
event's immutable `github.sha`, not the mutable tag name. The generated
workflow also serializes runs for the same app. A workflow that deliberately
supplies another `ref` bypasses these push-event checks.

Set `rollout: "safe"` for a balanced health-gated canary. The action submits
the canary, waits for readiness, and then waits for rollout completion when
`wait: "true"`; long waits renew a short-lived GitHub OIDC identity between
polling windows. Safe rollout is available on Pro/Scale plans and is reported
in the step summary and Check Run title.

## Pin reproducibility

The action is pinned to `@v0` during public beta. The `release.yml` workflow force-updates the `vN` moving tag on every `vN.M.P` release, so `@v0` always resolves to the latest vendored binary. For full immutability, resolve that moving tag once and pin its commit SHA:

```bash
git ls-remote https://github.com/poyrazK/faas.git refs/tags/v0
```

```yaml
- uses: poyrazK/faas/.github/actions/deploy@<40-character-commit-sha>
```

The bundled `cli-version` output lets you lint for drift in enterprise monorepos.

## Security

- GitHub OIDC is the default: grant `id-token: write` and the Action exchanges the job JWT for a five-minute Gregale bearer. A long-lived `api-key` remains an optional fallback.
- Long waits renew that short-lived identity between polling windows; the bearer is never written to an Action output.
- Connect the GitHub App and bind the repository to the Gregale app once. The first Action run then verifies GitHub's JWT and creates a trust policy pinned to that exact repository subject and audience.
- The bearer token is set via the `FAAS_TOKEN` env var inside the run step and never appears in `$GITHUB_OUTPUT` or `::error` annotations.
- The `src/annotate.sh` step regex-redacts any `gh*_`, `Bearer …`, or `FAAS_TOKEN=…` substring from error annotations as a defence-in-depth against server regressions.
- No PAT, GitHub App token, or install token is required at the customer side. The Gregale control plane resolves the install token server-side from the account's `github_installations` row (`ADR-012`, `ADR-020`).

## Failure modes

| Server response | What it means | What to do |
|---|---|---|
| `409 source_ref_unavailable` | Transient githubd or codeload blip. Server sets `Retry-After: 30`. | Re-run the workflow. |
| `404 github_install_not_found` | The account has no `github_installations` row. | Run `gregale connect` on a workstation once. |
| `413 source_too_large` | Repo tarball exceeds the per-plan `SourceTarballMaxMB` cap. | Trim history or upgrade plan. |
| `400 invalid_ref` | `--ref` is not a branch, tag, or 7+/40-char SHA. | Pin to a SHA. |
| `429 plan_limit_*` | Per-plan concurrency / RAM cap reached. | Wait for a slot, or upgrade. |

## License

MIT. See [LICENSE](LICENSE).
