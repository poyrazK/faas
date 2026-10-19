# CI deployments to private previews (internal, default off)

CI can stage prebuilt output and obtain a private immutable preview with a
credential limited to one site. The workflow does not promote production.
See [ADR-202610170925](adr/202610170925-site-scoped-hosting-ci-previews.md), the
[preview guide](internal-frontend-hosting-previews.md) and
[manual release workflow](internal-frontend-hosting-releases.md).

## Enable and issue access

Apply `20261017092513070_hosting_automation_credentials.sql` and upgrade **all** API
writers before enabling runtime `frontend_hosting_automation_enabled`, bootstrapped
by `FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED=1`. It defaults off. Terraform output
also leaves it `0`. Main hosting, durable routing and private previews require
their existing independent opt-ins. Source labels need history enabled too.
The site must use durable routing, and the account must remain an internal
allowlisted operator. Existing account/MFA/scope checks apply; an automation
bearer does not perform interactive MFA on each job.

From an operator workstation with its existing login:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED=1
gregale sites tokens create --site SITE_UUID --label github-preview \
  --operations stage,preview --ttl 24h --out ./hosting-ci.token
gregale sites tokens list --site SITE_UUID
gregale sites tokens revoke --site SITE_UUID --credential CREDENTIAL_UUID
```

Creation prints metadata only and saves plaintext to a **new mode-0600 file**;
it refuses existing paths. Move it into your CI secret manager without printing
it, then remove your local copy. There is no token retrieval endpoint. If issuance
has an uncertain response, inventory and revoke the possibly issued credential
before minting again. No plaintext is saved in idempotency receipts or audit data.

Credentials use prefix `fp_host_` with 256 random bits, SHA-256 hashed at rest.
Supported operations are `stage`, `preview`, or both. At most 16 stored
credentials/site; expired entries are pruned when creating another. Default
lifetime is 24 hours, bounded from five minutes to 30 days. Rotate explicitly.
Revocation is checked against storage on every subsequent API request, including
retries. In-flight work and existing five-minute signed R2 upload grants may
finish. Revoking a CI key does not revoke its preview URLs or Access identities.

A site credential can read its site's details/status and capabilities, stage
releases when granted `stage`, and create/list previews when granted `preview`.
It cannot promote, suspend, delete, mint/revoke keys, manage domains, read history
or access other sites/apps/account resources. Unknown routes are denied by
default. Changing the credential's site or operations requires a new credential.
No new dashboard credential form or public entitlement is introduced.

## Deploy prebuilt files

Install a trusted pinned Gregale CLI containing `sites ci`; previously released
binaries may not contain this command. In a trusted job, supply the site secret
through `FAAS_TOKEN`, never as an argument, and set the main/automation workstation
flags above. Enable `FAAS_FRONTEND_HOSTING_HISTORY_ENABLED=1` for source options.

```bash
gregale sites ci --site SITE_UUID --run-id STABLE_RUN_UUID --path ./dist \
  --preview-ttl 24h --wait-timeout 2m \
  --source-repository org/storefront \
  --source-commit 0123456789abcdef0123456789abcdef01234567 \
  --source-branch main
```

The CLI requires an HTTPS API origin (loopback HTTP is accepted for local tests),
a site-scoped token with **both** operations, and existing static manifest/file
bounds. It checks advertised scope/capabilities before allocating a run. Uploads
use separate credential-free clients, HTTPS and exact staging paths with no
redirects. No build or per-site runtime runs on Gregale servers.

JSON output contains `run`, `state`, `published` and, after acknowledgment,
`preview_url`. It preserves known identities on failure and exits nonzero until
both preview and parent-site publication receipts match their intended
generations. `publication_acknowledged` means the provider accepted publication;
it does not prove global propagation or serving health. The URL remains protected
by the existing internal Access policy; CI gets no Access bypass credential.

Wait defaults to two minutes, maximum ten; the whole command has a 20-minute
bound. Preview TTL is five minutes through 72 hours (24-hour default), measured
from the **first accepted run**, including upload time. A rerun never extends it.

## GitHub Action

Use the [composite Action](../.github/actions/frontend-hosting/README.md) with
`enabled: 'true'`, a one-site secret, the site UUID, output directory and explicit
trusted `cli-path`. It uses runner Python 3 and a preinstalled CLI. Pin the Action
revision and CLI source/release in the calling workflow. Its default run UUID
includes API/site/repository/GitHub run ID and excludes `GITHUB_RUN_ATTEMPT`.
Use a distinct explicit run UUID for each site/output deployment if calling the
Action multiple times in the same workflow run for the same site.

Only expose credentials to trusted workflows/build artifacts. The Action does
not run a build, fetch a repository, post comments/checks, or use GitHub write
permissions. GitHub repository/SHA/branch values are **unverified source labels**,
not provenance attestation. Changed output/labels on the same run conflict.

## Retry, limits and storage

Keep the same run UUID, manifest, provenance and TTL on every retry. Rebuilds
that change bytes/metadata require a deliberately new run. Interrupted allocation,
upload, finalize or preview creation resumes the retained identities. Concurrent
retries allocate one release/preview pair; they can still perform duplicate
uploads or publication attempts. Do not automatically use a fresh ID on failure.

The API stores run intent transactionally, rather than relying on generic
idempotency receipts. Operators can inspect it with:

- `PUT /v1/internal/hosting/sites/{site}/ci/{run}`: accept/reuse immutable request
  `{manifest, provenance?, preview_ttl_seconds}` and return the run.
- `GET /v1/internal/hosting/sites/{site}/ci/{run}`: inspect the original IDs.
- `POST /v1/internal/hosting/sites/{site}/ci/{run}/preview`: create/reuse its preview.

The release/preview IDs survive their physical catalog cleanup in the run ledger.
An expired, revoked, retired or removed resource is never replaced for that run;
inspect it and explicitly start a new intent when appropriate. Pending releases
and preview holds retain their existing bounded quotas and cleanup rules. CI
cannot delete them. Operators manage cleanup through the existing workflow or the separate [default-off retention policy](internal-frontend-hosting-retention.md). Retention protects the original CI window and preview references, and never removes run identities.

This pilot retains at most **1,000 run identities/site**, with no automatic
pruning. Retiring releases does **not** free run slots. Existing runs still retry
at that limit; new intents return `hosting_ci_run_quota_exceeded`. Review pilot
lifecycle/capacity with an operator; never delete ledger rows to force a retry.
Only final site purge cascades the identities. The limit must be revisited before
long-lived/high-volume rollout. Inventory/limits are exposed by capabilities.

There are at most 16 small credential rows and 1,000 run rows/index entries/site,
plus ordinary release/preview storage. No per-site resident RAM/CPU, extra edge
lookup, artifact copy or build worker is added. Disabling automation blocks its
keys/endpoints immediately at admission and preserves stored intent, serving
previews and cleanup. Existing main/preview serving switches remain separate.
Downgrade refuses while credentials or run identities remain; archival and pilot
retirement are deliberate operations, never automatic metadata deletion.

Local tests cover real API authorization/hot disable/revocation, memory/Postgres
concurrency, atomic rollback, quota and catalog cleanup, lost CLI responses, exact
upload grants, secret-safe Action outputs and stable GitHub reruns. Live
Cloudflare qualification remains deferred; no live hosting resources are changed.

For short-lived GitHub identity exchange and automatic renewal without stored Gregale secrets, see the separately gated [OIDC guide](internal-frontend-hosting-oidc.md).

Read-only [deployment diagnostics](internal-frontend-hosting-diagnostics.md) explains retained site and CI intent, with explicit recovery guidance and a separate default-off flag.
