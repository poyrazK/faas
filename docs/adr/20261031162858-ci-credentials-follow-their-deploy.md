# ADR-20261031162858: CI credentials can read the app they deploy and follow the deployment

- **Status:** accepted
- **Date:** 2026-10-31
- **Amends:** ADR-034 rev2 (scoped API keys): `GET /v1/apps/{slug}` also
  admits `deploy:write`. It also widens the deploy-token route binding in
  `pkg/auth/middleware`, which no ADR recorded: deploy tokens may read
  their own app's deployments.
- **Checked against:** `CLAUDE.md` component ownership (apid only reads
  here; nothing new is written), `docs/release-compatibility.md` (the server
  change also fixes released CLIs, including the one the deploy Action
  vendors), and the `tests/journeys` known failure
  `TestImageRegistryAndDeployTokens/cli_deploy_with_token` (2026-10-27).

## Context

Customers deploy from CI with three kinds of credential that carry only
`deploy:write`:

- a **deploy token** (`fp_deploy_…`), bound to one app and limited to routes
  under `/v1/apps/{slug}` and `/v1/uploads`;
- the **OIDC bearer** the official deploy Action exchanges a workflow's
  identity for (`pkg/oidc`, deploy-only by default);
- an account **API key** created with `--scopes deploy:write`, which
  `docs/agents.md` and the CI docs recommend.

None of them could complete a CLI deploy:

1. **Every CLI deploy reads its app first.** `gregale deploy`
   (`createOrFetchAppWith`) and `gregale deploy --repo --ref`
   (`ensureSourceRefApp`, which the deploy Action runs) call
   `GET /v1/apps/{slug}` to create the app or reuse it. That route required
   `admin` or `apps:read`, and scopes match exactly, so each credential got
   `403 insufficient_scope` before anything was uploaded. Production saw it
   on 2026-10-27 (issue #5756 item 4). The raw `POST /v1/apps/{slug}/deployments` worked.
2. **A deploy token cannot follow its deployment.** `--wait` and
   `gregale deployment wait` poll `GET /v1/deployments/{id}`, which
   `api.ScopesDeploymentReadSurface` opens to `deploy:write` exactly "so a
   least-privilege CI token can poll the deployment it just created". The
   deploy-token binding still refused every path outside `/v1/apps/` and
   `/v1/uploads`.
3. **The human-mode wait gave up.** `deploy --wait` streams build logs
   (`/v1/deployments/{id}/logs`) and falls back to `/v1/builds/{id}`; both
   need `apps:read`. The CLI then read the deployment once and, finding it
   still building, exited 3 "stream unreachable". The build-status poll also
   retried its 403 until its deadline (5 s up front, 60 s at the stream's end).

## Decision

1. **`GET /v1/apps/{slug}` admits `deploy:write`**
   (`api.ScopesDeployTargetReadSurface`). It reveals nothing new:
   `deploy:write` can already `PATCH /v1/apps/{slug}` and `POST /v1/apps`,
   which answer the same document, and read any deployment. A deploy token
   stays bound to its own app by the middleware's `{slug}` check. Other app
   reads (logs, metrics, secrets lists, audit) keep `apps:read`.
2. **A deploy token may read `GET /v1/deployments/{id}` and
   `GET /v1/deployments/{id}/status` for a deployment of its own app.** The
   middleware matches the route pattern, validates the id, resolves the
   deployment's app through an optional `DeploymentAppResolver` on the store
   adapter, and answers `404 no such deployment` for another app's
   deployment or a malformed id, the same shape as a cross-account probe.
   No other `/v1/deployments` route opens to deploy tokens; build logs and
   the account-wide list stay refused.
3. **The CLI follows the deployment row when it may not read build logs.**
   `deploy --wait` keeps polling the deployment to the wait deadline when
   the log stream cannot open, as it already did when the stream ended
   early, and says it is following the deployment's status. A 401 or 403
   ends the build-status poll and the deployment poll at once instead of
   retrying it.

Nothing new is stored, and no scope is added or renamed.

## Consequences

- `FAAS_TOKEN=<deploy token> gregale deploy --image … --wait` and the deploy
  Action in its default OIDC mode deploy and wait to the end. The journey's
  `cli_deploy_with_token` can leave `known_failures.json` once a release
  with this change is on production.
- A `deploy:write` key can read the metadata of every app in its account,
  as it can already update each of them. Customers who want one app only
  use a deploy token.
- A CI wait without `apps:read` shows no build log lines. The deployment's
  status and failure reason still arrive, and `gregale logs` with a broader
  key shows the build.

## Not decided here

- Streaming build logs to `deploy:write`. Build output can echo
  environment values a deploy credential should not otherwise read.
- An app-scoped deployment read (`/v1/apps/{slug}/deployments/{id}`), which
  would let the binding stay purely path-based.
