# Internal frontend hosting

This is the internal static-site pilot from
[ADR-20261015](adr/20261015-internal-static-hosting.md). It is disabled by
default. It deploys prebuilt files without a running customer VM. There is no
public hosting entitlement, customer billing integration, or production SLA.

## Configure an isolated internal deployment

The [Terraform pilot configuration](../deploy/cloudflare/hosting-worker/terraform/README.md)
automates the resources below with serving and DNS/routing disabled by default.
Its outputs supply non-secret bootstrap settings and the live qualification
deployment identity. Use Terraform or the manual procedure below as the sole
owner of Worker configuration.

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
or existing timeout Worker routes. The Terraform configuration changes cloud
resources only on an explicit apply; attaching routes and enabling the Worker
require separate explicit inputs. With manual provisioning, deploy only after
performing the same isolation and Access checks.

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
python3 scripts/ci/test_internal_frontend_hosting_qualification.py
```

The PostgreSQL suite requires the normal isolated test fixture. A live pilot
must additionally prove denied unauthenticated/cache-hit requests, disable
propagation, real R2 conditional publication, interrupted uploads, promotion,
rollback, and retirement on its own configured Access/Worker/bucket resources.

## Live qualification

Use `scripts/ops/internal-frontend-hosting-qualification.py` against an applied,
dedicated Terraform pilot and an existing disposable Gregale project and
environment. The runner creates a fresh `hosting-qual-*` site; it never updates
an existing customer site. It needs Python 3.12+, a Gregale CLI built from this
hosting implementation, Terraform, and AWS CLI v2 for authenticated R2 prefix
inventory. The API must already have the hosting migration, provider settings,
operator allowlist, and feature flag. The Worker/route must already be enabled.
The script does not bootstrap credentials or enable API admission.

Set the following credentials in the operator environment, without writing
them into reports or tfvars:

| Variable | Purpose |
|---|---|
| `FAAS_API` | Explicit HTTPS origin for the pilot's Gregale API; there is no production default |
| `FAAS_TOKEN` | MFA-qualified operator credential for the expected disposable account |
| `FAAS_FRONTEND_HOSTING_ENABLED` | `1` to opt into the internal CLI on this workstation |
| `CLOUDFLARE_API_TOKEN` | Read access to the pilot's R2 domains and Worker subdomain settings; also deployment permissions if checking the Worker switch |
| `HOSTING_QUAL_ACCESS_TOKEN` | Unexpired user Access JWT for the wildcard application and an allowed internal email |
| `HOSTING_QUAL_R2_ACCESS_KEY` | Bucket-scoped R2 identity that can list objects for cleanup evidence |
| `HOSTING_QUAL_R2_SECRET_KEY` | Its S3 secret, passed only through the AWS CLI environment |

Obtain the user JWT with the existing Cloudflare Access login flow, for example
`cloudflared access token --app=https://qualify.YOUR_HOSTING_DOMAIN`, and bind its
output to `HOSTING_QUAL_ACCESS_TOKEN` without printing it. Service-token
credentials do not satisfy the Worker's user-email policy. Allow enough token
lifetime for propagation checks and at least the nine-minute-thirty-second
retirement grace. Use a fresh report destination on every run:

```bash
python3 scripts/ops/internal-frontend-hosting-qualification.py \
  --infrastructure deploy/cloudflare/hosting-worker/terraform \
  --binary ./bin/gregale \
  --account QUALIFICATION_ACCOUNT_UUID \
  --project hosting-qualification \
  --environment test \
  --evidence /tmp/hosting-qualification-us.json
```

The runner checks the output's storage fingerprint, Worker source digest, and
authenticated account before creating a site. It queries Cloudflare to reject
public R2 domains, `workers.dev`, or preview URLs. It exercises partial and
wrong-digest uploads, recovery with renewed grants, finalization retries,
immutable publication despite an old staging grant, two CLI deployments,
observed file-cache hits, unauthenticated denial after warming that cache,
SPA/asset/directory/HEAD/ETag behavior, old asset retention, stale-revision
promotion refusal, rollback, and retirement.

To also test the independent Worker switch, add `--check-worker-switch` **only
on a dedicated pilot**: this temporarily stops every site on that Worker. The
runner requires a clean Terraform plan, inspects each saved switch plan, and
permits changes only to the Worker version/deployment. It rejects unrelated
infrastructure/source/binding changes. The original enabled flag is reconciled
in a `finally` block even after an interrupted apply. Keep `worker_enabled =
true` in the reviewed local tfvars; do not modify infrastructure concurrently.

After the scenario, including failures and handled interrupts, the runner
requests site deletion, waits for routing denial and metadata removal, and
independently verifies that both the staging and published site prefixes are
empty using authenticated R2 listings. Cleanup has a 15-minute default timeout.
A successful deletion response alone does not qualify cleanup. The private
0600 report records site/release IDs, completed checks, observed propagation
times, cache results, and Cloudflare location codes; no token, signed URL, or
child-process output is recorded. Existing evidence files are never overwritten.
If a process is forcibly killed or cleanup fails, use the recorded site ID
(or generated site name if creation lost its response) to retire/delete it and
verify both prefixes manually. Restore a failed Worker switch before continuing.

Repeat from a real European runner with a new evidence path to compare actual
edge locations and cold/warm observations. These are individual observations,
not a global latency percentile or origin-residency guarantee. Reports explicitly
list untested API/control-plane outages, regional load, and an omitted Worker
switch. They never qualify public launch. The existing short routing leases
and lack of customer billing/domain onboarding remain pilot boundaries.
