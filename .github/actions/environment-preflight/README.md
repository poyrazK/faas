# Gregale environment preflight Action

Run a source-environment qualification and ask Gregale whether promotion to a
target environment is currently allowed. A failed qualification or blocked
promotion preview fails the Action step, so downstream promotion jobs can use
`needs` as the promotion gate. This Action records a qualification and reads a
promotion preview; it never promotes or deploys.

```yaml
name: Promote staging to production

on:
  push:
    branches: [staging]

jobs:
  qualify:
    runs-on: ubuntu-22.04
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v4
      - id: preflight
        uses: poyrazK/faas/.github/actions/environment-preflight@v0
        with:
          project: shop
          from: staging
          to: production
          profile: gregale-qualification.yaml

  promote:
    needs: qualify
    runs-on: ubuntu-22.04
    environment: production
    steps:
      - uses: actions/checkout@v4
      - name: Install Gregale CLI
        run: curl -fsSL https://get.gregale.dev | sh
      - name: Promote the qualified release set
        env:
          FAAS_TOKEN: ${{ secrets.GREGALE_PROMOTION_TOKEN }}
        run: gregale projects environments promote shop --from staging --to production --yes --wait
```

The qualification profile is checked into the repository and defines health
and smoke GET probes for each workload in the active source release set:

```yaml
version: 1
timeout_seconds: 5
workloads:
  api:
    health_path: /healthz
    smoke_path: /ready
```

For OIDC, the `qualify` job needs `id-token: write`. The Action requests the
closed `environment-preflight` capability, whose short-lived bearer has only
`project_environments:read` and `project_environments:qualify`; it cannot
deploy, promote, read secret values, or change configuration. The existing
trust policy must accept the job's GitHub issuer, subject, and audience. An
optional `api-key` input may be used instead, but it must be scoped to exactly
those read/qualify operations.

## Inputs

| Input | Required | Default | Description |
|---|---:|---|---|
| `project` | yes | — | Gregale project slug. |
| `from` | yes | — | Source environment to qualify. |
| `to` | yes | — | Target environment checked for promotion. |
| `profile` | yes | — | Path to the qualification YAML file. |
| `sync-config` | no | `false` | Include non-secret source configuration in the promotion preview. |
| `api-key` | no | — | Narrowly scoped bearer; OIDC is preferred. |
| `oidc-audience` | no | `gregale` | Audience requested from GitHub and Gregale. |
| `api-base` | no | `https://api.gregale.dev` | Gregale API base URL; HTTPS is required except for localhost. |

## Outputs

`status` is `ready`, `blocked`, or `failed`; `qualification-id` identifies the
recorded source release-set qualification; `can-promote` and
`approval-required` mirror the server preview; `blocking-reasons` is a JSON
array; and `cli-version` identifies the bundled CLI. A ready preflight does
not imply human approval. Put the downstream job in a protected GitHub
environment if reviewers must approve it. That job uses its normal promotion
credential and the `gregale projects environments promote` command; the server
recomputes policy and validates the current qualification during promotion.
Do not replace this with a fresh `gregale deploy` if the intent is to promote
the artifact already running in staging.
