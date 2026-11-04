# Source deploys: GA release notes

These notes cover the customer-visible changes in
[ADR-20261026152731](adr/20261026152731-source-deploy-ga-hardening.md) and
the Dockerfile `HEALTHCHECK` enforcement of
[ADR-20261024100000](adr/20261024100000-dockerfile-healthcheck-enforced.md).
Publish them with the release that ships both. The customer reference is
[Deploy from source](deploys.md#what-gets-uploaded).

## Behaviour changes to call out

- **Dockerfile `HEALTHCHECK` now gates readiness.** The next deployment of a
  Dockerfile that declares a command `HEALTHCHECK` serves only after the check
  passes, and an instance whose check keeps failing restarts. A check that
  cannot run in the image (no `curl`, wrong port) now blocks the deployment
  instead of being ignored. Running deployments are unaffected until
  redeployed. Opt out with `HEALTHCHECK NONE` or by removing the instruction;
  for a Compose service, `healthcheck: {disable: true}`.
- **GitHub pushes get the accept-time checks.** A pushed repository that
  carries a database file under `data/` or `db/`, or a known credential
  format, is now refused like a CLI upload; the check run names the file.
- **Builds from a Dockerfile keep build output.** `dist/`, `.next/`,
  `target/`, `coverage/` and `.cache/` are uploaded when the app builds with a
  Dockerfile, and `.dockerignore` applies. Add a `.dockerignore` if these
  directories should stay local.
- **Retries have a 24-hour window on every install.** After 24 hours,
  `gregale deploys retry` reports that the source is no longer available;
  deploy again from source.

## Fixes

- Migrations, schemas and seed files under `data/` or `db/` deploy; only
  database files there are refused.
- Heuristic secret findings (high-entropy strings, Stripe test keys) no
  longer block a deploy; they are listed on the deployment. Subresource
  Integrity digests are never flagged, and `gregale:allow-secret` on a line
  keeps a deliberately committed value.
- `.gregaleignore` can re-include a default-excluded directory with `!dir/`,
  and Go `vendor/` directories ship.
- Committed symlinks, submodules and Git LFS files deploy as the content they
  stand for, with a warning naming the fix when something is not available
  locally.
- The CLI applies the server's limits: the plan's upload limit to the packed
  archive and 2 GiB to the expanded source.
- `--mcp-task-migration-override` is no longer dropped.
- `gregale.yaml` `build:` selects a Dockerfile and passes build arguments, a
  target stage and build secrets for single-app deploys.
- Turning off source deploys for an account no longer hides image deploys or
  deployment status.
