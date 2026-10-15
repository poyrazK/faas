# Internal frontend hosting

This is the internal static-site pilot from
[ADR-20261015](adr/20261015-internal-static-hosting.md). It is disabled by
default. It deploys prebuilt files without a running customer VM. There is no
public hosting entitlement, customer billing integration, or production SLA.

## Configure an isolated internal deployment

Provision a private R2 bucket and a dedicated Cloudflare KV namespace. Keep the
bucket private: disable public bucket access and public custom-domain bindings.
Create a Cloudflare Access application covering every hostname on a separate
hosting domain. Use a different registrable domain from the Gregale dashboard
and application domain; a subdomain of `gregale.dev` does not provide that
isolation. Restrict Access to internal identities.

Configure these through apid's existing secret environment mechanism:

| Variable | Purpose |
|---|---|
| `FAAS_FRONTEND_HOSTING_ENABLED` | `1` to bootstrap the default-off runtime switch |
| `FAAS_FRONTEND_HOSTING_DOMAIN` | Isolated hosting domain, without a scheme or wildcard |
| `FAAS_FRONTEND_HOSTING_R2_ENDPOINT` | HTTPS S3 endpoint for the private R2 bucket |
| `FAAS_FRONTEND_HOSTING_R2_BUCKET` | Dedicated bucket name |
| `FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY` | Bucket-scoped publication identity |
| `FAAS_FRONTEND_HOSTING_R2_SECRET_KEY` | Publication secret; never put it in the repository |
| `FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID` | Cloudflare account ID |
| `FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID` | Routing namespace ID |
| `FAAS_FRONTEND_HOSTING_CF_TOKEN` | Token scoped to writes in that account's routing KV |
| `FAAS_ADMIN_EMAILS` | Existing API operator allowlist; empty denies every operator |

The runtime console setting is `frontend_hosting_enabled`. Provider configuration
is bootstrap configuration and requires a restart. Enabling the flag without
providers does not make the API available. This pilot does not require enabling
the separate customer S3 preview.

Copy the Worker's [example configuration](../deploy/cloudflare/hosting-worker/wrangler.toml.example)
to an operator-owned configuration file. Bind the same private R2 bucket and KV
namespace. Set `ACCESS_TEAM_DOMAIN`, `ACCESS_AUD`, and `INTERNAL_EMAILS` to the
real Access application and permitted internal identities. The Worker verifies
the JWT signature and claims itself; the presence of an Access header is not
enough. Set `ORIGIN_ID` to the site's returned `origin_id` (the SHA-256 digest of
the exact R2 endpoint, a newline, and bucket name).

After Access and DNS are ready, attach only the isolated hosting zone's
wildcard route and explicitly set the Worker's
`FAAS_FRONTEND_HOSTING_ENABLED = "true"`. Its example disables `workers.dev`
and preview URLs. Do not attach it to the dashboard, API, customer S3 endpoint,
or existing timeout Worker routes. No repository command automatically
provisions resources, changes DNS, or deploys the Worker.

Apply migrations through Gregale's normal deployment process. Add a storage
lifecycle rule expiring `hosting/staging/` after one day. Published objects
must remain until Gregale retires them. Changing the endpoint/bucket fingerprint
disables old sites; migrate explicitly rather than repurposing their backend.

## Deploy and roll back

Enable the internal CLI command on an operator workstation and authenticate
with an operator account owning the project:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
gregale sites create --name storefront --project shop --environment production
gregale sites deploy --site SITE_UUID --path ./dist --spa
gregale sites releases --site SITE_UUID
gregale sites rollback --site SITE_UUID --release RELEASE_UUID
gregale sites retire --site SITE_UUID --release UNUSED_RELEASE_UUID
gregale sites delete --site SITE_UUID
```

The environment must already exist. Site operations emit JSON, including the
hostname, origin fingerprint, active release, and revision. Promotion returns
202 when intent is committed; wait for edge propagation before checking the
hostname. It does not mean every Cloudflare location has switched immediately.

Deploy hashes a real directory, rejects links/special files, uploads directly
with temporary grants, verifies publication, and promotes against the revision
read at deployment start. A concurrent deploy yields a conflict rather than
silently replacing newer intent. A failed deployment leaves a release ID for
inspection/retirement. The internal API also supports refreshing upload grants
for a pending release; ready releases cannot issue new grants.

Root HTML follows the active release. Directory indexes are supported. `--spa`
only falls back for extensionless requests accepting HTML; missing JavaScript
and API-style JSON requests return 404. Retained releases can serve old asset
paths absent from the current manifest. Use fingerprinted asset names: reusing
one URL for different bytes cannot preserve an old browser's intended version.
An exact release is available at `/_gregale/releases/RELEASE_UUID/index.html`;
asset URLs under that prefix stay pinned to it. Customer content is not rewritten.

Browser responses require revalidation. The Worker's shared cache uses immutable
site/release/path/digest keys and is consulted only after authorization and
routing checks. Only GET/HEAD are supported; SSR, redirects configuration,
range responses, and custom domains are not included in this pilot.

## Disable and recover

Disable `frontend_hosting_enabled` to reject new API operations immediately.
Enabled edge routes expire within 120 seconds of their last refresh. Also set
the Worker's switch to `false` when stopping the pilot. Cache entries cannot
bypass either gate. Previously issued staging grants expire within five minutes
and cannot modify published files. Cleanup of already-retired releases continues.

An API, KV publication, or database outage lasting past the lease stops serving
the pilot. Restore configuration/connectivity and allow reconciliation to renew
routes. Active release intent survives restarts. Check API audit records and
the structured hosting reconciliation log, Cloudflare Worker/Access analytics,
KV write usage, and bucket usage. Do not expose signed URLs or provider tokens
in diagnostic output.

Deleting a site stops its route and retires all releases. Cleanup waits at
least nine minutes thirty seconds, then removes catalogued staging/published
objects and metadata. Failed operations retry. Retire/delete sites before
deleting their project/account; foreign keys protect the cleanup catalog.
Published data has no automatic age-based expiration, and CDN bytes are not
yet part of customer billing. Keep this feature internal.

## Portable checks

```bash
go test -race ./pkg/hosting ./pkg/apid/hosting ./pkg/state/hostingtest
go test ./cmd/gregale -run 'Test(StaticHosting|HostingUpload|SitesCommand)'
node --test --test-isolation=none deploy/cloudflare/hosting-worker/worker.test.mjs
```

The PostgreSQL suite requires the normal isolated test fixture. A live pilot
must additionally prove denied unauthenticated/cache-hit requests, disable
propagation, real R2 conditional publication, interrupted uploads, promotion,
rollback, and retirement on its own configured Access/Worker/bucket resources.
