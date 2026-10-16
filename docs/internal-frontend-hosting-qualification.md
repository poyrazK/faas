# Internal frontend hosting acceptance qualification

The [hosting runner](../scripts/ops/internal-frontend-hosting-qualification.py)
extends the [dedicated pilot procedure](internal-frontend-hosting.md#live-qualification).
Fault injection, regional sampling, resource profiling, quota probes, domain
lifecycle, and traffic observations are opt-in. The
runner never enables API admission, deploys a Worker, provisions a region, or
selects a production API by default. Use the migration, API/CLI, and Worker from
the same checkout. Durable responses must report `X-Gregale-Route-Generation`.

## Run regional observations

Run this on an actual US runner, then on an actual European runner with a new
evidence path and the appropriate location label:

```bash
python3 scripts/ops/internal-frontend-hosting-qualification.py \
  --infrastructure deploy/cloudflare/hosting-worker/terraform \
  --binary ./bin/gregale \
  --account QUALIFICATION_ACCOUNT_UUID \
  --project hosting-qualification \
  --environment test \
  --edge-samples 30 \
  --runner-region us-east \
  --evidence /tmp/hosting-us-observations.json
```

Use a newly created durable site with both durable-routing switches enabled.
The runner publishes a separate retained release with unique HTML paths. These
files and their manifest have not been requested before cold probing; varying
query parameters would not make the Worker's immutable file cache cold. It
waits for the publication receipt and observes that generation at the edge
before probing the new release. Every cold request must report `MISS`; repeated
requests are classified by their actual `HIT`/`MISS` header, and at least one
of each must be observed.

Reports contain each request's observed wall time, cache result, generation,
and Cloudflare location, plus sample counts and nearest-rank p50/p95 by cache
result. The runner-region label is operator supplied; it does not verify the
machine's geographic location. The Cloudflare location is read from `CF-Ray`.
These are bounded observations from those runners, not a global percentile or
load test. At most 100 unique regional paths are created per run.

`r2_file_reads_minimum` counts observed file-cache misses. It excludes manifest
and route reads, repairs, uploads, inventory, retries, and storage charges. The
report keeps `provider_usage_billing` unqualified; use actual Cloudflare usage
and invoices for service costs.

## Supply an isolated pilot controller

An operator-owned executable supplies the host-specific fault mechanism. This
separates the qualification scenario from SSH, systemd, firewall, and container
management, and prevents the runner from guessing a production service or
database. Configure and review that executable for the actual dedicated pilot
before opting into faults. The executable path must be absolute.

Every response is a JSON object, at most 16 KiB, with this identity:

```json
{
  "version": 1,
  "api_origin": "https://api.pilot.example",
  "account_id": "QUALIFICATION_ACCOUNT_UUID",
  "origin_id": "ORIGIN_FINGERPRINT",
  "worker_name": "gregale-internal-pilot"
}
```

The values must match the explicit API, disposable account, and applied
Terraform output. No API/Access/R2/Cloudflare/AWS publication credentials are
passed to this executable. It inherits basic process/proxy/CA settings and
`SSH_AUTH_SOCK` when configured. Child output is captured, never printed or
copied into the evidence report. Commands are executed as argument arrays,
without a shell.

Implement these commands in addition to those identity fields:

| Command | Additional response fields and required behavior |
|---|---|
| `describe` | `dedicated: true`, `modes: ["api", "database", "publication"]` for supported faults; optional `capabilities: ["resource_profile"]` |
| `enter MODE RUN_ID DURATION` | Acquire exclusive fault ownership for this run; apply and independently verify the scoped fault; return `mode`, `run_id`, and `automatic_restore_at` as Unix seconds |
| `status MODE RUN_ID DURATION` | Return `mode`, `run_id`, `fault_active: true` only after checking the actual fault; also `api_running: true` for database/publication isolation |
| `leave MODE RUN_ID DURATION` | Idempotently restore this run's fault, release its ownership, and return `mode`, `run_id`, `fault_active: false` |
| `snapshot` | Optional fresh process/resource observation, described below |

`enter` must install an independent watchdog before changing the host. Its
automatic restoration time must be between `DURATION + 30` and `DURATION + 300`
seconds from now; allow enough of that margin for controller/network latency.
Use the full five-minute margin for slow links. The watchdog must work if the
runner is forcibly killed. Refuse an overlapping run, and never let `leave`
clear another run's fault or unrelated firewall/configuration state. All
commands must complete within 45 seconds. Keep credentials in the existing
protected operator mechanisms; never log them or return them in JSON.

Scope each mode narrowly:

- `api`: stop or isolate the dedicated pilot apid. Keep its Worker, R2, Access,
  and external test runners available.
- `database`: deny only this dedicated apid's database access while apid stays
  running and reachable. Do not stop a shared PostgreSQL service.
- `publication`: deny only apid's R2 publication access. Keep API/database
  management and Worker's R2 access working.

The runner verifies data-backed API reads fail during API/database faults;
database isolation must return a server error through the still-reachable API.
It also checks the controller's independently verified fault state repeatedly.
Publication isolation must produce an actual pending generation with a stored
provider failure and retry count. A controller assertion by itself cannot
qualify these phases.

## Exercise faults and recovery

On that dedicated pilot, add the flags below to the regional command:

```bash
--outage-controller /absolute/operator/pilot-controller \
--check-control-plane-outages \
--check-publication-recovery \
--outage-duration 1200 \
--outage-interval 30
```

API and database faults run separately for at least twenty minutes each.
Each phase uses a fresh, finalized release whose manifest and files remain
unrequested until the outage. The runner observes exact expected bytes from
both the warmed active release and unique cold paths throughout the fault,
then verifies recovery. It requires a real cache hit as well as cold misses.
Outage duration is bounded to 1200–3600 seconds and probe intervals to 5–60
seconds, keeping each release under the hosting file limit.

Publication recovery commits two promotions while apid cannot reach R2. The
first intent is superseded by the second; the previously accepted head keeps
serving. After restoration, the latest generation must be acknowledged and
observed at the edge. The normal scenario then tests suspension, resumption,
retirement, deletion denial, artifact cleanup, and retained terminal heads.

Allow at least an hour for the full default run, including propagation and
cleanup, with an Access JWT valid beyond that window. Run fault phases on one
runner at a time. Regional observations may be repeated separately on the
other runner without fault injection.

The runner records mode/run ID before entering a fault, attempts restoration
in `finally` even if entering loses its response, and tries restoration again
before cleanup if necessary. An unsuccessful restoration makes the report
fail. After a forced kill, inspect the private report, confirm watchdog
restoration, use idempotent `leave` for its recorded run if needed, and clean
up the recorded site. Do not qualify a run with missing recovery evidence.

## Record control-plane resources

Add `--resource-profile` with the same dedicated controller to collect before
and after-cleanup observations. Its `snapshot` command must include the
identity fields above and these fields:

```json
{
  "sampled_at": 1792080000,
  "apid_instance_id": "process-start-identity",
  "resources": {
    "apid_cpu_seconds_total": 12.5,
    "apid_resident_memory_bytes": 67108864,
    "hosting_database_bytes": 65536
  }
}
```

Use actual apid process/cgroup CPU and resident-memory measurements, and actual
allocated SQL relation/index/TOAST bytes for the hosting tables. The timestamp
must be within 60 seconds. Change the instance ID whenever the cumulative CPU
counter resets. Nonfinite CPU counters, invalid byte counts, and foreign/stale
observations are rejected; unknown JSON fields are not copied into the report.

The report records raw samples and database footprint change. It computes CPU
delta only when both samples describe the same process counter without a reset;
otherwise cross-restart CPU accounting remains unqualified. These are process
and hosting-table observations on the dedicated pilot, including background
work, not per-site CPU attribution or a billing implementation. File storage
and CDN usage remain separate provider costs.

## Run the extended acceptance scenarios

The runner also supports independently opt-in account accounting, custom-domain
lifecycle, and traffic observations. Every normal run now checks unauthenticated
GET, HEAD, and conditional GET before a unique path is fetched and after its
file cache has an observed hit. Authorized HEAD and 304 behavior remain part of
the static HTTP contract.

Run the portable failure fixtures without credentials or infrastructure:

```bash
python3 -m unittest discover -s scripts/ci -p 'test_internal_frontend_hosting_*.py'
```

These fixtures exercise the runner's decisions, including lost DNS command
responses, ambiguous provider creates, missing terminal heads, wrong Access
audiences, early storage release, and malformed or unavailable traffic reports.
They do not establish actual DNS propagation, certificate issuance/renewal,
Cloudflare entitlements, analytics ingestion, or regional performance. The same
fixture command runs in the hosting workflow. No optional scenario enables a
feature flag, deploys infrastructure, or buys a provider service.

For a later, explicitly provisioned live pilot, add any of these options to the
base command above:

```bash
--check-usage \
--check-traffic \
--custom-domain pilot.external-qualification.example \
--domain-controller /absolute/operator/pilot-domain-controller
```

Use the same checkout for API, CLI, Worker, and qualification scripts. Do not use
a previously reserved hostname: removing a domain retains its reservation and
terminal head permanently. Use a fresh disposable external subdomain for each
run. DNS for a Cloudflare-hosted customer domain must be in the separate account
described in the [custom-domain runbook](internal-frontend-hosting-domains.md).

### Account quota and cleanup evidence

`--check-usage` requires an empty, exclusively owned disposable hosting account.
Preflight refuses existing release reservations before creating a site; keep
other writers out for the entire run. The scenario creates manifests declaring
the advertised maximum release size, without requesting upload grants or
uploading their bytes. With the current pilot limits it admits five 100 MiB
release manifests, retaining their two-copy-plus-manifest reservations.

It proves the pending-upload cap with a separate zero-payload manifest, retires
one pending release to open a slot, and checks that retirement keeps its byte
reservation. It then proves the storage cap with the next large manifest.
Both 409 responses must include the expected stable code, limit, attempted
value, and documentation URL. Storage rejection takes precedence when both
limits would be exceeded. The quota fixture's releases are retired and their
catalog cleanup is awaited before the normal deployment scenario starts.
This adds another retirement grace period to a live run.

After finalization, rollback, and retirement, the runner compares account usage
with the manifest sizes and states in the exclusive site's catalog. It retries
non-atomic reads that race background cleanup. Ready releases must no longer
occupy pending slots; retiring releases still reserve bytes. After site removal
and authenticated empty artifact-prefix listings, all six catalog counters must
return to zero. Fixed routing/domain tombstones are intentionally outside this
account reservation check. These observations do not measure physical bucket
bytes, upload bandwidth/concurrency, or provider billing.

### Custom-domain controller protocol

`--custom-domain` and `--domain-controller` must be supplied together. Preflight
requires API domain capability, applied Worker domain infrastructure and serving,
and durable routing. The domain and CNAME target must match the isolated pilot.
The controller must attest to an unused, exclusively owned disposable hostname;
it must refuse existing or unrelated records, provider objects, and overlapping
runs. The [bundled pilot adapters](internal-frontend-hosting-pilot.md) implement this
contract for Cloudflare customer DNS and protected operator JWT files. Review
and configure their isolated scope before running this scenario.

The controller is invoked as `[ABSOLUTE_EXECUTABLE, COMMAND]`, without a shell.
Each command receives a JSON object on standard input, with this identity:

```json
{
  "version": 1,
  "api_origin": "https://api.pilot.example",
  "account_id": "QUALIFICATION_ACCOUNT_UUID",
  "origin_id": "ORIGIN_FINGERPRINT",
  "worker_name": "gregale-internal-pilot",
  "hostname": "pilot.external-qualification.example",
  "run_id": "QUALIFICATION_RUN_UUID",
  "records": [],
  "access_aud": ""
}
```

Return the seven identity fields unchanged in a JSON object of at most 16 KiB.
Commands have a 45-second timeout. As with the outage controller, only basic
process/proxy/CA settings and `SSH_AUTH_SOCK` are inherited. No runner API,
canonical Access, Cloudflare, R2, or AWS credentials are supplied. Use protected
operator mechanisms for the controller's own DNS and Access authorization.
Child output, tokens, proof challenges, and record values never enter evidence.

| Command | Required response and behavior |
|---|---|
| `describe` | Read-only; `dedicated: true`, `disposable_hostname: true`; verify this exact unused hostname and pilot, refuse overlap |
| `sync` | `applied: true`; idempotently acquire run ownership and add/retain only the supplied DNS-only CNAME, Gregale proof TXT, and bounded provider-validation records; journal ownership before writes |
| `token` | `access_token: "JWT"`; obtain a valid Access identity for this exact hostname and supplied application audience, without disabling or bypassing Access |
| `clear` | `cleared: true`; idempotently remove only records journaled as owned by this run, release ownership; never remove another run's or pre-existing records |

The runner derives the Gregale TXT/CNAME itself and rejects provider record
names outside the disposable hostname. Subsequent syncs must retain earlier
validation records until `clear`; certificates may depend on them. The runner
persists the hostname/run ID and marks DNS cleanup required *before* any sync,
so an applied write with a lost response still has a recovery identity. The
controller must retain its journal across crashes; after a forcibly killed
runner, use its recorded identity to finish provider/site cleanup and then
idempotent `clear`. Do not automatically clear validation DNS on a timer while
a live or uncertain provider object still depends on it.

After DNS, TLS, Access, and alias publication are active, the controller obtains
the domain-specific JWT. The runner observes exact HTML, an actual file-cache
hit, authorized HEAD/304, and denial of anonymous and canonical-audience cookies.
It repeats the serving checks after promotion and rollback, and verifies
suspension through the alias. On removal it observes cached content revocation
while DNS and the certificate still exist, then waits for the API domain row to
be purged. It verifies the owned terminal R2 domain head before clearing DNS.
It does not require successful HTTPS after provider cleanup: the removed
hostname may no longer have a certificate.

On failure the runner requests site deletion in its existing `finally` path.
DNS is cleared only after site/provider catalog cleanup and an owned terminal
head can be confirmed. If those checks fail, evidence retains `dns_cleanup:
"required"` and recovery identity; preserve DNS and follow the
[standard-plan recovery procedure](internal-frontend-hosting-domains.md#retry-remove-and-recover).
A `provider_conflict` stops the normal scenario. The normal scenario never chooses an
unbound provider ID, resets a create marker, or invokes recovery. The separately
opted-in provider-failure scenario below permits one recovery request only after
a run-bound operator review.
A successful normal lifecycle does **not** qualify ambiguous-create recovery,
domain-provider outages, or certificate renewal.

### Traffic observation evidence

`--check-traffic` requires the independent API analytics capability and applied
Worker analytics binding/collection flags. After generating fixture traffic, it
reads the site's no-store traffic endpoint and requires the canonical site ID,
Analytics Engine source, approximate marker, valid sampling weight, fresh UTC
24-hour window, ordered hourly buckets, matching bucket totals, and fixed
bounded failure groups. Only counters and maximum sample weight are saved.

An empty report is retried within `--edge-timeout`; it cannot prove collection
is working. A provider/API error fails this scenario and cannot become zero
traffic. Allow enough time for the first write, provider ingestion, and the API's
one-minute query cache when selecting the timeout. There is no exact request or
byte assertion: sampling, ingestion delay, and other authorized pilot traffic
make those totals approximate. These checks alone do not qualify provider-query
outage/recovery, actual delivered bytes, CPU, R2 operations, or billing. Follow
the independent [traffic runbook](internal-frontend-hosting-traffic.md) for
credentials, privacy, retention, and provider charges.

## Qualify provider failures and reviewed recovery

Three additional scenarios are independently opt-in. They use a separate
operator-owned provider fault controller, alongside the existing DNS/Access
controller. The [bundled private pilot relay](internal-frontend-hosting-pilot.md) implements
this controller contract behind an explicit bootstrap opt-in. Provider credentials
remain operator supplied. The portable fixture suite exercises all three without
live provisioning.

For a later explicitly provisioned, isolated durable pilot, add the desired
flags to the extended acceptance command:

```bash
--provider-controller /absolute/operator/pilot-provider-controller \
--check-domain-recovery \
--domain-recovery-review /absolute/private/new-provider-review.json \
--check-domain-provider-outage \
--check-analytics-outage
```

The two domain scenarios require `--custom-domain` and its DNS controller.
Analytics isolation requires `--check-traffic` and the existing independent
analytics feature switches; it can run without a custom domain. Invalid flag
combinations, an existing review file, and a missing controller fail before site
creation. All product flags remain independent and default off.

`--provider-timeout` defaults to 2400 seconds, bounded to 180..3600. An active
domain is normally reconciled only every 30 minutes: the runner waits for that
scheduled provider call and does not modify SQL schedules or accelerate the
reconciler. Select a timeout that covers that interval. After a fault is actually
observed, `--provider-observation-seconds` defaults to 180, bounded to 180..600.
Allow at least two hours for the complete live run, plus operator review time,
and renew API/Access identities as needed. The bundled pilot supports protected
JWT files because the current Access apps have one-hour sessions.

### Scoped provider fault controller

Invoke `[ABSOLUTE_EXECUTABLE, COMMAND]` without a shell. Each command receives
this JSON identity on standard input and must return **all fields unchanged**:

```json
{
  "version": 1,
  "api_origin": "https://api.pilot.example",
  "account_id": "QUALIFICATION_ACCOUNT_UUID",
  "origin_id": "ORIGIN_FINGERPRINT",
  "worker_name": "gregale-internal-pilot",
  "site_id": "SITE_UUID",
  "domain_id": "CLAIM_UUID",
  "hostname": "pilot.external-qualification.example",
  "mode": "domain_api",
  "run_id": "FAULT_RUN_UUID",
  "duration": 2700
}
```

For read-only `describe`, the site/domain IDs, mode, run ID and duration are
empty/zero because the site has not been created. A hostname may be empty for
analytics-only qualification. After creation, each fault's exact scope is
persisted before its first command, and kept unchanged for restoration and
inspection. Refuse mismatched identities and overlapping faults. Commands have
a 45-second timeout and a 16 KiB JSON response bound. They inherit only the same
basic process/proxy/CA/SSH-agent environment as the DNS controller. They receive
no runner API, Access, Cloudflare, R2 or AWS credentials. Never return raw request
bodies, SQL, tokens or upstream errors in evidence fields.

| Command | Required behavior and additional response |
|---|---|
| `describe` | Read-only independent pilot verification; `dedicated: true`, `modes` listing supported names below |
| `enter` | Journal exclusive scope and install an independent watchdog before applying the fault; return `automatic_restore_at` as Unix seconds |
| `status` | Independently inspect the scoped fault; `fault_active: true`, `api_running: true`, plus actual request/object counters |
| `leave` | Idempotently restore only this run's changes; `fault_active: false`; safe when enter applied but lost its response, or never applied |
| `inspect` | Read-only retained journal and provider activity for this exact run, even after leave; `fault_active: false` after restoration and the counters below |

Watchdog restoration must be between `duration + 30` and `duration + 300`
seconds from the enter response. Duration covers the initial timeout,
observation window, and a two-minute command margin. Preserve restoration if
the runner is killed. Do not change feature flags, Access policies, the serving
Worker, tenant DNS, PostgreSQL availability, or Worker R2/JWKS connectivity to
simulate a provider management failure.

| Mode | Fault and independent evidence |
|---|---|
| `create_response` | For this exact new hostname, let the provider create succeed, record its actual object, then lose the response to apid. Count **all** create requests for that hostname through restoration/inspection. Return `create_requests`, `provider_objects`, `dropped_responses`; each must remain exactly 1. Keep the journal armed for read-only counting after restoration. |
| `domain_api` | Isolate only apid's custom-hostname/Access management API traffic for this bound claim. Leave existing edge Access enforcement, DNS/TLS, Worker, R2 and the main API available. Count real failed management requests in `failed_requests`, and successful management requests after restoration in `successful_requests_after_restore`. |
| `analytics_query` | Isolate only apid's Analytics Engine SQL requests for the canonical site in this scope. Leave collection, the main API and serving intact. Count real failed queries and successful queries after restoration in the same two counters. |

These are pilot-specific mechanisms, such as an independently monitored scoped
provider proxy. Configure them using protected operator mechanisms. A controller
assertion alone cannot qualify: the runner also requires actual API conflict/error
state, successful management reads, and exact authorized serving while Access
continues denying anonymous requests.

### Lost create-response recovery

The fault is armed before Gregale's proof TXT/CNAME are installed. Once the API
reports the durable create marker, an unbound provider ID, and `provider_conflict`,
the runner observes the whole configured window. It requires another completed
reconciliation attempt without another create, implicit binding, or marker/revision
change. Canonical serving must continue. This window also exceeds the current
160-second recovery fence; the API independently enforces that fence.

The fault is restored and independent counters rechecked before operator review.
Evidence then contains `domain_recovery_review.status: "required"` and its
`identity` object: version, API/account/storage origin/Worker, site/claim/hostname,
fault run ID, provider origin and expected revision. Follow the
[standard-plan recovery procedure](internal-frontend-hosting-domains.md#retry-remove-and-recover)
to review provider activity, ownership, account/zone, hostname and exact object
UUID. Copy that identity **unchanged** into the new review JSON file and add only
`"provider_id": "REVIEWED_PROVIDER_UUID"`. Write a temporary regular file and
atomically rename it to the configured absolute review path. The runner rejects
stale/foreign/extra fields, symlinks, oversized or malformed reviews; it never
obtains a candidate UUID from the fault controller or hostname search.

The runner submits one revision-fenced recovery POST. If its transport response
is lost, it reads the claim for the exact reviewed provider and the next revision;
it never repeats the POST. A rejected or uncertain unconfirmed request fails the
scenario and needs fresh review outside that run. Success requires DNS/TLS/Access
and serving recovery, plus another independent proof of exactly one provider
create/object/dropped response. Preserve uncertain claim state and DNS if recovery
or cleanup cannot complete.

### Bound domain-provider and analytics outages

For `domain_api`, the runner waits for the normal scheduled reconciliation to
report `provider_unavailable` with an increased completed-attempt count. The bound
provider/Access IDs and published alias generation must remain unchanged. During
the observation window it checks exact warm canonical/alias bytes, anonymous Access
denial, and separate unique cold paths through both hostnames. After restoration,
a later successful reconciliation must clear the error on the same binding; the
controller must observe an actual successful provider request.

For `analytics_query`, a cached successful report cannot qualify the outage.
The runner waits for the actual no-store 503 `hosting_traffic_unavailable` after
query-cache expiry and independently recorded failed queries. Disabled-feature or
API errors do not qualify. Exact warm and cold serving continues through the
observation window. After restoration, the full approximate traffic report must
validate again with nonzero observations and a successful new provider query.
Unavailable traffic never becomes zero traffic or a serving dependency.

Provider faults restore in `finally`, including applied writes with lost command
responses. A failed restoration is retried at the run boundary. Until restoration
is acknowledged, site deletion and DNS cleanup are **blocked**, and the report
retains the fault identity and recovery action. After a forced kill, inspect all
`provider_faults` journal identities, confirm watchdog restoration and idempotent
leave for the outstanding run, then finish site/provider cleanup before clearing
owned DNS. Each completed check references its fault run ID; only complete
failure, serving, and recovery evidence removes that scenario from `not_run`.

## Interpret acceptance reports

Reports now use `version: 2`. `status: "passed"` means the selected scenarios
and cleanup completed. Each optional scenario remains in `not_run` until its
own checks finish. `public_launch_qualified` remains false. Review `not_run`
even when the selected run passes: ambiguous provider-create recovery,
domain-provider and analytics-provider outages, certificate renewal, actual
provider usage/billing, and unselected regional/control-plane scenarios remain
explicit release gates. Fixture success never fills these live evidence gaps.
