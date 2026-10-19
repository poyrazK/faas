# Migrate existing services

In the console, choose **New app → Import → Migrate from GitHub**. Scan the
repository and select up to ten source-backed HTTP or GraphQL services, such as
a frontend and API. Review their dependencies and startup commands. Worker,
cron and container-image workloads stay separate from this workflow.

Review the suggested Gregale app names before deploying. A repository service
called `api` can become `shop-api`, while its configuration and dependencies
still use `api`. Edit new app names and choose **Check app names** to check
availability. Editing a name requires another review; an existing project
service keeps its current Gregale name. Scanning reserves no names, so deployment
checks availability again. No repository rename is required.

Keep the existing database and storage. Paste or import a separate `.env` for
each service. For server-side service URLs, choose an existing variable such as
`API_URL` and explicitly connect it to a selected declared dependency such as
`api`. Gregale configures `https://api.internal` without repository edits. Each
service can map up to ten variables. Other keys keep their supplied values.
All supplied and mapped values are sealed before any selected build is
queued. Detected environment names are suggestions; the repository's
`.gregale/required-secrets.json` declares requirements. A declaration inside a
workload directory overrides the repository declaration.

Deploy once to save one project migration with every selected service and its
exact deployment. The URL contains a migration UUID and optional source labels,
never secret values. The project's **Saved migrations** section reopens progress
after closing the page. Accepted deployments continue through the existing
build/runtime workers. Refreshing progress never submits another deployment.

Use **Review unfinished services** to retry failed or unaccepted work. Supply
configuration corrections only for those services. Re-enter any originally
supplied keys that were not saved before a configuration failure; the server
requires all of them before starting builds. Accepted services are
preserved. Re-scan the original repository revision; the server checks its saved
source checksum. Archives expire after one hour and may be lost on an API
restart, so pin the original commit when a mutable branch moves. Already queued
builds have their own durable source. If an app or deployment was changed outside
the migration, review it before creating another migration.

Saved migrations retain the reviewed service-to-app names even when the first
apply stops before creating an app. Resume sends those saved names when it
re-scans; changing a migration's target names requires a separate migration.
They also retain runtime connection choices. Resume restores those choices
automatically; pasted values for mapped keys are not submitted. For browser and
build-time variables such as `VITE_API_URL` and `NEXT_PUBLIC_API_URL`, use the
public frontend URL controls. Explicitly choose a selected dependency's reviewed
public Gregale endpoint or supply an existing public HTTPS base URL. Review the
resolved value and confirm it is public before deployment. These public values
are saved, displayed on reload, and supplied during the first build and retries.
They cannot use private runtime addresses.

Public URL keys use `VITE_`, `NEXT_PUBLIC_`, `REACT_APP_`, `PUBLIC_`, `NUXT_PUBLIC_`
or `GATSBY_` and end in `_URL`. Up to ten URLs per service are supported, with a
2048-byte limit per URL and a 24,000-byte encoded map limit. URLs may include a base path; credentials, queries and
fragments are rejected. Values can appear in browser bundles. Configure CORS and
authentication in your API for the frontend's origin. Dockerfiles must declare
matching `ARG` variables in the build stage. Changing a public URL requires a
new reviewed migration and rebuild; runtime secret edits do not rebuild bundles.
If an existing runtime secret or a different runtime value uses the same key,
remove or correct that setting first so it cannot override the public URL at
runtime. The migration reports this conflict before queueing builds.

Migration scans keep automatic GitHub deployments, schedules and async routes
off. Production traffic stays with the previous provider until each service is
verified and its cutover is explicitly performed. Service-level checks document
their own coverage and do not certify the entire dependency graph.

The connected scan accepts `app_slugs`, for example
`{"api":"shop-backend","web":"shop-frontend"}`. Each returned workload has its
original `name` and reviewed `app_slug`. Omitted mappings receive suggestions in
migration mode. Keep `only` and `initial_secrets` keyed by repository names.

The API workflow uses a migration-mode connected project scan, followed by
`POST /v1/project-migrations` with a client-generated canonical `id`, the reviewed
`plan_token`, selected `only` names and optional `initial_secrets` and
`service_connections`, for example `{"web":{"API_URL":"api"}}`. Map only selected
declared dependencies; do not also supply a mapped key in `initial_secrets`. The same
create ID and request replay the saved operation. Inspect/list with
`GET /v1/project-migrations/{migration}` and `GET /v1/project-migrations?project=slug`.

For public frontend build URLs, creation accepts `public_service_connections`,
for example `{"web":{"VITE_API_URL":"api"}}`, or explicit `public_build_urls`,
for example `{"web":{"NEXT_PUBLIC_AUTH_URL":"https://auth.example.com/v1"}}`.
Each mapped target must be a selected declared dependency with a public endpoint
in the reviewed plan. The saved service receipt exposes its resolved
`public_build_urls` and the optional logical `public_service_connections`.
Resume sends neither field: the server restores the saved values. Each queued
deployment captures its own inputs, and build caches change with those inputs.
Public endpoints route normally and do not prove a specific API deployment or
browser CORS behavior; exercise the frontend/API flow before cutover.
Resume explicitly with `POST /v1/project-migrations/{migration}/resume`, a token
for the same source, the reviewed `expected_version`, exact unfinished `only`
names, and optional configuration corrections. Stale or replayed resume actions
are rejected so later failures cannot expand the reviewed action. Supplying
values or mapping connections additionally requires secret write permission. Receipts retain key names,
exact deployment IDs and last recorded verification/cutover references.

The existing `POST /v1/projects/apply-plan` remains available for ordinary apply
clients. CLI project `--secrets-file` bundles are also sealed before any builds
are queued. A configuration failure starts no new build and leaves saved apps
and completed configuration available for correction.

After the deployment becomes live, select **Verify your migration**. Supply a
read-only HTTP path that exercises the application, such as `/ready`. Checks
require a successful 2xx response from that exact deployment through the Gregale
gateway. Choose dependency checks using saved environment names:

- Runtime service connections: guided verification automatically includes saved
  mappings and the migration's current target deployment IDs once targets are
  live. The guest checks the variable's private HTTPS value and proves authorized
  routing to that exact deployment through Gregale's read-only platform probe.
  A changed target blocks previous evidence. Choose an application smoke path
  that also exercises the real application calls.
- PostgreSQL: the connection-string key, such as `DATABASE_URL`; the guest opens
  an authenticated connection and runs `SELECT 1`.
- Object storage: an existing S3 binding variable prefix, such as `ASSETS`; the
  guest performs a read-only listing with its six injected binding variables.
- External API: a key containing a complete HTTPS URL for a read-only GET.
  Redirects and non-2xx responses fail; response bodies are not retained.

Cold-wake verification briefly parks the Gregale service, waits for every active
instance to drain, then checks a correlated wake and repeats HTTP/dependency
checks. Keep production traffic with the previous provider during this step.
Omitting dependencies or cold wake is shown as a coverage limit on the report.
These checks do not copy data, compare application datasets, or prove schema
rollback safety.

Progress is saved and driven on the server. You can close the console while
checks run. The API worker finds unfinished receipts after a restart, waits for
any previous worker lease to expire, and resumes the same task and wake IDs.
The console polls reports; a failed refresh does not stop verification.
Infrastructure failures retry with bounded backoff. Use **Reload reports** to
read current progress after a connection interruption.
Changes to the deployment or runtime configuration invalidate prior evidence.
Download the JSON report when checks finish.

Choose **Cancel verification** to stop a pending run. Cancellation is saved
before the server stops tasks and recovers a service the run may have parked.
A run remains pending during cleanup, and another run cannot overlap it.
The ten-minute deadline stops checking; recovery continues beyond that deadline
until tasks have stopped and service recovery has an observed outcome. A wake
request alone does not count as restoration. The report records a running
service as `restored`, a replacement deployment as `superseded`, or an account
or security restriction as `blocked`. Resolve a blocked recovery in the app or
account controls. Recovery never wakes a replaced or quarantined deployment.

SDK/API callers create a receipt with
`POST /v1/apps/{slug}/migration-verifications`, supplying a stable UUID,
`deployment_id`, `checks`, `smoke_path` and explicit `cold_wake` consent. Poll
`GET /v1/apps/{slug}/migration-verifications/{run}` or list app receipts. Cancel
with `POST /v1/apps/{slug}/migration-verifications/{run}/cancel`; repeating the
same cancellation is safe after a lost response. The optional `/advance`
operation remains available for compatibility, but clients do not need to drive
work. Only one unfinished run, including cleanup, is allowed per app, and the
latest twenty reports are discoverable. Deployment write scope, verified email
and MFA protect creation, advance and cancellation; reads require app read scope.
Cancellation remains available if the host disables new task admission.

After verification passes, use **Switch production traffic** to prepare the
production hostname. Before editing DNS, copy every current routing record for
that hostname from your DNS provider, including IPv6, TTLs and the CDN proxy
setting. Enter one routing record per line, for example
`CNAME old.provider.example 300` or `A 203.0.113.10 300`. Choose error-rate and
p95 latency budgets for the verified GET path. **Prepare production switch**
saves this plan and leaves traffic unchanged. Download a copy for recovery.
Return to saved plans through the app's **Deployments** tab, including after
leaving the import page.

Choose **Add this domain to Gregale** if needed. This uses normal domain quotas
and ownership verification. Publish the supplied TXT ownership record first.
When ready, replace the old routing with the supplied CNAME, or use the A/AAAA
alternatives when your DNS provider requires them. Choose one routing method,
disable the CDN proxy for the hostname, and keep unrelated DNS records in place.
Lowering the old routing TTL in advance and waiting out its previous TTL can
reduce propagation time. Keep the previous provider available while caches update.

Choose **I updated DNS — start checks** after publishing routing. Checks continue
on the server for up to thirty minutes, even with the console closed. They check
domain ownership, current routing, a trusted HTTPS certificate and a successful
response with Gregale gateway proof for the exact verified deployment. The old
provider's successful response or certificate cannot pass this check. TLS may
take a few minutes once routing reaches Gregale; inspect domain diagnostics for
DNS, CAA and proxy issues. An attention state permits restarting the checks.

Once hostname checks pass, review observed traffic and choose **Complete
production switch**. Completion checks the hostname again. Traffic health uses
the existing monitor's completed windows for this deployment and exact GET route
after hostname proof. It does not change saved monitor policies. These aggregates
are not specific to the hostname and cannot prove that every customer moved.
Sparse or unavailable telemetry, including plan-gated telemetry, remains unknown
and requires explicit acknowledgement before completion. Sustained error or
latency budget violations block completion. A changed deployment, runtime
configuration or domain binding blocks the saved evidence; verify the new setup
and prepare another plan. Saved recovery records remain visible.

To recover, open **Restore previous provider**. At the DNS provider remove
Gregale routing for the hostname, restore all saved routing records and TTLs,
and restore the saved CDN proxy setting. Check the previous service and allow
cached DNS to expire. The confirmation records your acknowledgement of these
manual steps; Gregale does not edit provider DNS or automatically restore it.
**Cancel cutover checks** stops checking and preserves the recovery settings.

The cutover API is `POST /v1/apps/{slug}/migration-cutovers`, with a stable UUID,
`verification_id`, `hostname`, `previous_records`, `previous_proxy_enabled`,
`max_5xx_rate_bps` and `max_p95_ms`. List or get saved plans under the same path.
Submit actions to `POST /v1/apps/{slug}/migration-cutovers/{plan}/actions` with
`action` set to `start_checks`, `complete`, `cancel` or `confirm_restored` and
`acknowledge_limited_traffic` explicitly set. Preparation and reads do not start
probes. Twenty plans are retained per app; terminal plans may be pruned after
ninety days on subsequent creation. Download plans needed for longer retention.
Creation/actions require deployment write scope, verified email and MFA; reading
requires app read scope and MFA. Domain enrollment uses the existing domain API.

If a frontend fetches API data while compiling (for example Next.js static page
generation), select **APIs needed while building** for its declared API services.
This is optional: leave it clear when the build only needs to bake in a URL.
Configure the public endpoint separately; private runtime connections are not
available inside a build.

Gregale waits for each API deployment selected by the saved migration to become
live with 100 percent traffic before starting the frontend build. Waiting uses no
builder slot and survives closing the console or restarting the worker. The
console displays the blocker and a fixed 30-minute deadline. Cancel a waiting
deployment from its app. Fix a failed API and use **Review unfinished services**;
retrying the frontend preserves the healthy API deployment and saved choices.
If an API changes outside this migration, review its app and create a new reviewed
migration. Public requests still route normally, and readiness is a check at build
admission rather than a guarantee about later requests or data freshness.

The API create body accepts `build_dependencies`, for example
`{"web":["api"]}`. Targets must be selected, declared migration dependencies;
resume cannot replace saved choices. A frontend-only retry keeps the selected
healthy API ID. Build artifact caches include the selected dependency IDs;
Railpack compile inputs change with those IDs and gated Dockerfile builds rerun
layers. Normal later source deploys do not automatically inherit these migration
waits. Native acceptance must cover Next.js data fetching and warm build behavior
before release.
