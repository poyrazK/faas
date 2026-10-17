# Internal frontend hosting

This is the internal static-site pilot from
[ADR-20261015](adr/20261015-internal-static-hosting.md). It is disabled by
default. It deploys prebuilt files without a running customer VM. There is no
public hosting entitlement, customer billing integration, or production SLA.

## Customer release visibility

Keep the effective API runtime setting `frontend_hosting_enabled=false`. Its
`FAAS_FRONTEND_HOSTING_ENABLED` environment value only seeds runtime configuration;
check for persisted overrides. The entire disabled API namespace returns the
ordinary missing-route 404, including anonymous, wrong-method and OIDC requests.
Workstation CLI help, manuals, suggestions and execution require local opt-in.

Build the customer dashboard with `NEXT_PUBLIC_FRONTEND_HOSTING_ENABLED=false`
(or unset), then run `npm run verify:hosting-hidden` in `faas-frontend`. Disabled
builds exclude the hosting client implementation as well as hiding its link and
returning a non-cacheable 404. Changing only a running container's environment
cannot change an already-built public flag. Use fresh release artifacts and retire
any previously published enabled build/assets through the hosting provider.

Worker `FAAS_FRONTEND_HOSTING_ENABLED` is a separate serving switch: keep it
`false`, with Terraform `worker_enabled=false` and `attach_route=false` for a
disabled deployment. An enabled internal pilot must stay on its isolated zone,
behind Access and the Worker's explicit internal identity allowlist; never add
customer identities. API disable alone does not stop existing durable serving.
Do not enable public R2 access, workers.dev or Worker preview URLs.

These checks hide the feature in released product surfaces. Public repository
source, internal runbooks and previously distributed enabled artifacts are not
made secret by feature flags. The deployment settings still need verification
before release; local tests do not establish live configuration.

See [the visibility decision](adr/202610171453-hide-disabled-hosting-surfaces.md).

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
| `FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED` | Separate default-off opt-in: create new sites with durable routing |
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
blocks management and reconciliation for old sites; their last accepted durable
heads can still serve from the old Worker/bucket. Stop that Worker before moving
backends, and migrate explicitly rather than repurposing their storage.

## Internal operations dashboard and browser uploads

The `poyrazK/faas-frontend` operations console has a default-off hosting page
on its `codex/internal-static-hosting` branch. Build a dedicated internal
operations deployment with `NEXT_PUBLIC_FRONTEND_HOSTING_ENABLED=true` and
`APID_BACKEND_URL` set to this pilot's API. This Next.js flag is evaluated at
build time; it hides the navigation and makes `/operations/hosting` return 404
when off. `/hosting` is the clean operations-host URL. This does not enable any
API or Worker flag and does not deploy the feature to the customer console.

The dashboard checks `GET /v1/internal/hosting/capabilities`, which has the same
default-off, operator, MFA, and read-scope gates as site reads. Its no-store
response provides the enforced limits from `pkg/api/limits.go` and the private
upload base URL, with no credentials. Existing ownership checks still apply
to every site/release operation. Project and environment selectors use the
signed-in account's registered resources. There is no account impersonation.

For direct browser uploads, explicitly set `dashboard_upload_origins` in the
[Terraform module](../deploy/cloudflare/hosting-worker/terraform/README.md#browser-uploads-independent-opt-in)
to the exact HTTPS operations-console origin and review/apply the plan. The
empty default installs no allowed origins. Only PUT/content-type are allowed;
GET/list/delete and wildcard origins are excluded. CORS is bucket-wide, not a
path authorization rule: private S3 authorization and five-minute staging-only
signed grants enforce each writable path. Removing all origins revokes CORS
again. The bucket remains private; the CLI needs no CORS configuration.

The operations origin must also pass apid's existing session-write origin
guard, normally `https://operations.<apps-domain>`. A preview hostname added
to CORS alone does not grant API access. Use a dedicated API and trusted HTTPS
operations hostname; do not point development previews at production.

The browser selects one prebuilt output folder containing root `index.html`,
validates paths/count/size/manifest limits, and hashes one file at a time. It
uploads directly to the configured private origin's exact staging paths with
cookies omitted, no Gregale authorization/organization headers, no referrer,
and redirects rejected. Host and Content-Length are supplied by the browser
from the URL/File, not manually set as forbidden headers. Grants are refreshed
before half their TTL expires; completed uploads are not repeated. Progress
counts completed files and bytes. Server finalization still verifies the
size/digest before promotion.

Publishing a staged release, rollback, and suspend/resume use the revision the operator reviewed;
conflicts never automatically retry with a newer revision. On cancellation or
an uncertain response, refresh the inventory: server work may have finished.
Verified releases can be reviewed and published; incomplete ones can be verified or retired.
Retired/deleting resources count against limits until safe cleanup finishes.
Publication is displayed separately from accepted intent and serving health;
legacy leased sites show untracked publication. Cloudflare Access sign-in for
site visits is separate from the Gregale session. No cloud configuration or
feature switch is changed merely by shipping the dashboard code.

## Deploy and roll back

The [stage, review and publish workflow](internal-frontend-hosting-releases.md)
adds stage-only deployment, immutable release comparisons, explicit reviewed
promotion and bounded durable-publication waiting. Complete its coordinated
writer/publisher upgrade before using staging. CLI deployment retains combined
publishing by default; the internal dashboard defaults to staging.


An independently gated [custom-domain and managed HTTPS pilot](internal-frontend-hosting-domains.md)
adds DNS verification, private Access applications, certificate status, and
safe domain removal for durable sites. Its management, dashboard, and Worker
switches all default off.

Enable the internal CLI command on an operator workstation and authenticate
with an operator account owning the project:

```bash
export FAAS_FRONTEND_HOSTING_ENABLED=1
gregale sites create --name storefront --project shop --environment production
gregale sites deploy --site SITE_UUID --path ./dist --spa
gregale sites releases --site SITE_UUID
gregale sites status --site SITE_UUID
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
routing checks. Only GET/HEAD are supported; SSR and range responses are not
included. Independently gated [release configuration](internal-frontend-hosting-config.md)
adds exact redirects, approved response headers and an HTML 404 page. Custom
domains have their own [managed HTTPS pilot](internal-frontend-hosting-domains.md).

## Disable and recover

Disable `frontend_hosting_enabled` to reject new API operations immediately.
Leased edge routes expire within 120 seconds of their last refresh. Durable
sites keep serving. Set the Worker's original switch to `false` when stopping
all traffic; cached files cannot bypass it. Previously issued staging grants
expire within five minutes and cannot modify published files. Reconciliation
and cleanup of already-retired releases continue.

An API, KV publication, or database outage lasting past the lease stops leased
sites. Durable sites retain their last published routing state during API or
database outages, including for cold requests. Restore configuration/connectivity
to publish pending changes. Active release intent survives restarts. Check API audit records and
the structured hosting reconciliation log, Cloudflare Worker/Access analytics,
KV write usage, and bucket usage. Do not expose signed URLs or provider tokens
in diagnostic output.

Deleting a site commits route revocation and retires all releases; a 202 response
does not prove edge propagation. Cleanup waits at least nine minutes thirty
seconds, measured from revocation acknowledgement for durable sites, then removes
catalogued staging/published objects and metadata. Failed operations retry. Retire/delete sites before
deleting their project/account; foreign keys protect the cleanup catalog.
Published data has no automatic age-based expiration, and CDN bytes are not
yet part of customer billing. Keep this feature internal.

## Durable routing: separate internal opt-in

[ADR-202610151515](adr/202610151515-durable-static-hosting.md) adds durable R2
routing heads. Keep both new switches off until every apid writer/controller
has this implementation and the new migration. Older binaries do not maintain
the publication outbox or cleanup guards and cannot safely manage durable sites.

1. Set Terraform `durable_routing_enabled = true` and review/apply the Worker
   change, or set the equivalent Worker variable
   `FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED = "true"` with manual provisioning.
2. Enable the independent API runtime setting
   `frontend_hosting_durable_routing_enabled` (bootstrap environment value `1`).
3. Create a fresh site and verify `durable_routing: true` in its response. The
   flag selects the mode only at creation. Existing sites stay leased; turning
   the creation flag off does not change existing durable sites.

The Worker reads `hosting/routes/HOSTNAME.json` before KV when its new switch is
on. Only an absent head permits KV fallback. Disabled, deleted, malformed, or
foreign-origin heads deny requests even when files are already cached. Durable
heads have no expiry; route metadata is cached for at most 60 seconds. R2, Access,
and the edge runtime remain serving dependencies. The original Worker switch
still stops every site independently of the API/database.

Use account-owned, revision-checked suspension to stop one durable site without
changing its active release:

```bash
gregale sites suspend --site SITE_UUID
gregale sites resume --site SITE_UUID
```

Management admission must be enabled for these commands. They return 202 after
committing intent. A provider outage delays publication and the last accepted
head continues serving; use the Worker emergency switch for an immediate global
stop. Once publication succeeds, allow the metadata-cache interval to propagate.

Durable sites use no KV heartbeat writes. Each route change persists an outbox
generation and publishes using conditional R2 writes; retries survive restarts
and stale writes cannot replace newer heads. Unchanged heads receive six-hour
repair reads, about 120 per idle site per 30-day month, without rewriting them.
Edge metadata-cache misses also incur R2 reads. No per-site VM is introduced.

Retirement cannot remove artifacts until the excluding route generation has
been acknowledged and aged through the cleanup grace. Deletion also waits for
an acknowledged terminal head before removing SQL metadata, including for empty
sites. Keep `hosting/routes/` outside all expiration rules: one small terminal
head per deleted site remains after cleanup to fence delayed publishers. Only
the staging and published-release prefixes should become empty.

Before reverting to an older binary, retire/delete every durable site, verify
artifact cleanup and edge denial, and turn off the new Worker switch. The down
migration rejects live durable rows. Restoring provider/database snapshots
independently or deleting terminal heads needs a separate recovery procedure.

## Publication status and monitoring

`gregale sites status --site SITE_UUID` calls the operator-only, account-owned
`GET /v1/internal/hosting/sites/{site}/status` endpoint. It returns the current
`site` intent and a separate `publication` object:

- `pending`: `generation` exceeds `published_generation`. Deployment or traffic
  changes are committed but the latest route has not been acknowledged.
- `published`: both generations match. R2 accepted or verified the head. Edge
  metadata can still need up to 60 seconds to refresh; this is not a global
  serving-health check.
- `untracked`: the site uses leased routing, which has no durable publication
  receipt. No successful edge publication is inferred for it.

The object includes `published_at`, `pending_since`, `last_attempt_at`,
`next_attempt_at`, `retry_count`, and a bounded `last_error_code` where present.
`origin_unavailable` indicates backend configuration mismatch or absence;
`publication_unavailable` indicates a retryable provider failure;
`stale_generation` requires inspecting a newer/conflicting provider head and
the recovery history. Preserve terminal heads. Provider response bodies, URLs,
and tokens never appear in this status.

New intent clears its predecessor's attempt/error fields while preserving the
age of continuously pending work. An older acknowledgement cannot clear newer
pending intent. A failed six-hour repair of an already-published generation can
show an error while its state stays `published`. Repair reads update the last
attempt time without changing the original publication timestamp.

Apply the publication-status migration and upgrade all writers/controllers
before admitting durable changes. For an existing internal durable pilot,
pause the old controller during that migration and resume with the upgraded
binary; its static delivery can continue. Pending work present at the metadata
migration starts its age measurement at migration time.

The private apid Prometheus registry exposes these metrics, without site,
account, release, hostname, or raw-error labels:

| Metric suffix after `apid_` | Meaning |
|---|---|
| `hosting_route_pending` | Sites whose latest durable generation lacks an acknowledgement |
| `hosting_route_oldest_pending_seconds` | Continuously pending age at the latest observation |
| `hosting_route_backlog_available` | Latest backlog read succeeded (`1`) or is unavailable/unobserved (`0`) |
| `hosting_route_backlog_observed_unixtime` | Time of the latest successful backlog observation |
| `hosting_route_publication_attempts_total{outcome}` | Publication/verification attempts, including unchanged repair reads |
| `hosting_route_publication_duration_seconds{outcome}` | Attempt duration through provider acknowledgement |
| `hosting_reconcile_passes_total{outcome}` | Successful, failed, or canceled controller passes |
| `hosting_reconcile_last_success_unixtime` | Latest successful full pass |

Check availability and observation freshness before interpreting backlog
gauges; failed reads preserve previous values. Counters measure attempts, not
R2 PUT charges. Configure pilot monitoring for sustained pending work, repeated
publication failures, and stale reconciliation. Metrics do not enable hosting
admission or provision resources.

## Portable checks

```bash
go test -race ./pkg/hosting ./pkg/apid/hosting ./pkg/state/hostingtest
go test ./cmd/gregale -run 'Test(StaticHosting|HostingUpload|Sites)'
node --test --test-isolation=none deploy/cloudflare/hosting-worker/worker.test.mjs
python3 -m unittest discover -s scripts/ci -p 'test_internal_frontend_hosting_*.py'
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
| `HOSTING_QUAL_R2_ACCESS_KEY` | Bucket-scoped R2 identity that can list objects and read routing heads for cleanup evidence |
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
promotion refusal, rollback, and retirement. It records the persisted routing
mode and exercises suspension/resumption for durable sites; enable both new
switches before qualifying that mode.

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
empty using authenticated R2 listings. For durable sites it also reads and
validates the retained terminal head outside those prefixes. Cleanup has a
15-minute default timeout.
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
switch. They never qualify public launch. Leased sites retain short routing
leases; customer billing and domain onboarding remain pilot boundaries in both modes.

Use the [hosting qualification procedure](internal-frontend-hosting-qualification.md)
to enable explicit API/database fault checks, publication retry recovery, actual
cold/warm measurements, optional dedicated control-plane resource snapshots,
account quota/cleanup assertions, custom-domain lifecycle, traffic observations,
and independently scoped provider-failure/recovery scenarios.
These phases remain unqualified unless their observations complete.

## Account storage accounting

The [storage and cost runbook](internal-frontend-hosting-usage.md) describes
account-wide reservations, pending upload caps, cleanup accounting and the
operations dashboard’s hypothetical provider-cost calculator.

## Traffic observations

The independently gated [traffic runbook](internal-frontend-hosting-traffic.md)
describes provider-side approximate observations, privacy, retention and the
operations panel. It is separate from catalog storage accounting and billing.

The [runnable pilot workflow](internal-frontend-hosting-pilot.md) bundles the
private DNS/Access and provider-fault adapters plus prepare/qualify/recovery-cleanup
commands. Its relay requires a separate default-off bootstrap opt-in.

Private immutable URLs for staged releases are documented in the [preview guide](internal-frontend-hosting-previews.md). They require separate default-off opt-ins.

The [release source and history guide](internal-frontend-hosting-history.md) explains optional caller-provided source labels and bounded accepted-promotion history, behind a separate default-off opt-in.

The [CI automation guide](internal-frontend-hosting-automation.md) covers site-scoped credentials, retained run identities and a default-off GitHub Action that stages prebuilt files into private previews.

The [retention guide](internal-frontend-hosting-retention.md) covers default-off per-site cleanup policies, protected releases and safe retirement of abandoned uploads and expired CI releases.

GitHub jobs can optionally use [explicit site OIDC trust](internal-frontend-hosting-oidc.md) instead of a stored Gregale CI secret. This requires its own default-off flag.

Read-only [deployment diagnostics](internal-frontend-hosting-diagnostics.md) explains retained site and CI intent, with explicit recovery guidance and a separate default-off flag.

Guided internal GitHub setup is independently default-off. See
[workflow generation and local preflight](internal-frontend-hosting-ci-setup.md)
for reviewable downloads based on an existing immutable site trust.

The separately flagged [deployment activity view](internal-frontend-hosting-activity.md)
lists retained CI intents with bounded pagination and links to diagnostics and
release review. It performs no mutations or live provider checks.

See [preview admission reservations](internal-frontend-hosting-preview-reservations.md) for default-off capacity admission and operator snapshots.

See [terminal CI cancellation](internal-frontend-hosting-cancellation.md) for default-off operator recovery of abandoned runs.

See [local frontend serving preview](internal-frontend-hosting-local-preview.md) for default-off, credential-free build inspection before upload.

See [deployment planning](internal-frontend-hosting-planning.md) for a default-off,
read-only comparison of local output and prospective capacity before upload.

See [staging from a saved plan](internal-frontend-hosting-planned-staging.md) for
the independent default-off connection from a reviewed report to frozen uploads.

See [staging recovery](internal-frontend-hosting-stage-recovery.md) for an
independent opt-in that continues an exact retained release without replacement.
