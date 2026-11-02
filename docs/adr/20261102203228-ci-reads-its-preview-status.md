# ADR-20261102203228: Pull request CI reads its preview's status without an account-wide key

- **Status:** accepted
- **Date:** 2026-11-02
- **Amends:** ADR-20261101100000 (OIDC deploy bearers follow deploy refs), ADR-101 (secretless deploys)
- **Context:** CI commonly waits for a pull request's preview and runs tests against it (`gregale preview wait`, `GET /v1/preview/{slug}/environment`). The routes accepted only account-wide reads: per-app deploy tokens and the app-bound OIDC bearers of ADR-20261101100000 were refused off `/v1/apps/{slug}`, and a `pull_request` run gets no deploy bearer at all. The only credential that worked was an account API key stored in CI secrets, which a same-repository pull request's workflow can read, so anyone who could push a branch could take a key with account-wide rights.
- **Decision:**
  1. A credential bound to one app (a per-app deploy token, or an OIDC bearer exchanged for that app) may read `GET /v1/preview/{slug}` and `GET /v1/preview/{slug}/environment` for previews whose production app is that app, authorized against the production app. Other apps' previews answer 404.
  2. A new OIDC capability, `preview-read`, issues a bearer whose only scope is `previews:read`, accepted by those two routes and nothing else. Under a trust policy Gregale created it is issued to any run of the repository, `pull_request` included, for an app bound to the repository, and is bound to that app. Under a policy the account wrote it follows that policy, as the other capabilities do. `previews:read` is not an API-key scope.
  3. Migration `20261102203228267` widens the exchanged-token scope check to admit `previews:read` (added `NOT VALID`, validated, old check dropped).
- **Consequences:** A pull request workflow can wait for its preview with `id-token: write` and no stored secret. The bearer reads preview status only: not the production app, its logs, settings or deployments. The status names the PR's workloads, commit and phase, which the PR's own check already shows.
- **Rejected alternatives:**
  - **Give the bearer `apps:read`.** On `/v1/apps/{slug}` it would read the production app's logs and settings from unreviewed code.
  - **Allow app-bound deploy tokens only.** A deploy token in a `pull_request` workflow's secrets is readable by the PR and deploys production.
