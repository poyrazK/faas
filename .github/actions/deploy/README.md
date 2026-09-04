# Gregale deploy action for GitHub Actions

Official GitHub Action for [Gregale](https://github.com/poyrazK/faas) — deploy an app from a GitHub Actions workflow using a pinned `gregale` CLI.

This action is part of the Gregale [control plane](https://github.com/poyrazK/faas) — issue #270 / ADR-093. It wraps the existing `POST /v1/apps/{slug}/deployments/source-ref` endpoint; no new server surface is required.

## Usage

```yaml
jobs:
  deploy:
    runs-on: ubuntu-22.04
    environment: production
    permissions:
      contents: read
      id-token: none
    steps:
      - uses: poyrazK/faas/.github/actions/deploy@v1
        with:
          api-key: ${{ secrets.GREGALE_API_KEY }}
          api-base: https://api.gregale.dev
          app: my-app
          # repo / ref default to ${{ github.repository }} / ${{ github.sha }}
          wait: "true"
```

## Generate a starter workflow

From the Gregale CLI inside any repo:

```sh
gregale deploy --github --name my-app > .github/workflows/deploy.yml
```

The CLI emits a copy-paste workflow file. When run inside an Actions runner (`GITHUB_REPOSITORY` + `GITHUB_SHA` env vars set), the snippet hard-codes those values; from a local checkout it emits the `${{ github.* }}` expressions so the same file is portable across repos.

## Inputs

| Input | Description | Required | Default |
|---|---|---|---|
| `api-key` | Gregale API bearer token. Use `${{ secrets.GREGALE_API_KEY }}`. | yes | — |
| `api-base` | Gregale API base URL. | no | `https://api.gregale.dev` |
| `app` | App slug to deploy. | yes | — |
| `repo` | OWNER/NAME of the source GitHub repo. | no | `${{ github.repository }}` |
| `ref` | git ref — branch, tag, or 40-char SHA. | no | `${{ github.sha }}` |
| `format` | Source format passed to the source-ref endpoint. | no | `tarball` |
| `wait` | If `true`, block until the deployment is ready (or fails). | no | `true` |
| `wait-timeout` | Maximum seconds to wait when `wait=true`. | no | `600` |

## Outputs

| Output | Description |
|---|---|
| `deployment-id` | The new deployment id (32-char hex). |
| `app-slug` | Echo of the input `app` slug. |
| `status` | Final deployment status: `ready` \| `failed` \| `cancelled` \| `timeout`. |
| `url` | URL of the deployment record on the control-plane API (`{api-base}/v1/apps/{slug}/deployments/{id}`). |
| `cli-version` | Bundled `gregale` CLI version (verifies the vendored binary). |

## Pin reproducibility

The action is pinned to `@v1` by default. The `release.yml` workflow force-updates the `vN` moving tag on every `vN.M.P` release, so `@v1` always resolves to the latest vendored binary — minor and patch releases ship without any workflow edit on the customer side. For full immutability, pin a specific tag:

```yaml
- uses: poyrazK/faas/.github/actions/deploy@v1.4.2
```

The bundled `cli-version` output lets you lint for drift in enterprise monorepos.

## Security

- The bearer token is set via the `FAAS_TOKEN` env var inside the run step. It is masked by GitHub Actions automatically and never appears in `$GITHUB_OUTPUT` or `::error` annotations.
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
