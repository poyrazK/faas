# Customer object storage preview

Gregale can manage object buckets on interchangeable managed providers
without operating storage nodes. Compute remains stateless: these are not VM
volumes. The dashboard's Storage page keeps object buckets separate from
snapshot/image-layer usage.
Architecture and launch boundaries: [ADR-151](adr/151-provider-neutral-object-storage.md).
Large-upload protocol: [ADR-158](adr/158-provider-neutral-multipart-uploads.md).

## Enable a qualified backend

1. Apply database migrations through the normal Gregale deployment process.
2. Copy the [S3 example](../deploy/object-storage.example.json) or
   [GCS example](../deploy/object-storage.gcs.example.json) to an operator-owned
   config path, e.g. `/etc/faas/object-storage.json`. Set the real namespace,
   provider placement, region, and exact browser origins. Use a dedicated
   upstream account/project, not one containing unrelated infrastructure buckets.
3. For `s3`, supply the named access/secret environment variables only to
   **apid, gatewayd-public, and s3-gatewayd** through the deployment's secret
   mechanism. For `gcs`, give those daemons Application Default Credentials
   (ADC) for the configured service account; do not create a downloaded key. Never put
   credentials in JSON, app envs, source control, URLs, or logs. Optional S3
   `session_token_env` supports temporary credentials; restart/rotate before
   their expiration.
4. Set `FAAS_OBJECT_STORAGE_CONFIG=/etc/faas/object-storage.json` for apid,
   gatewayd-public, and s3-gatewayd, then restart every replica with identical
   settings. Set
   `public_endpoint` to `https://s3.gregale.dev` and `public_region` to
   `us-east-1`; those customer-facing values are independent of the upstream
   provider endpoint and signing region. Loading the configuration does not
   enable provisioning or the data plane. Missing config disables object
   storage in apid and prevents s3-gatewayd from starting. Invalid config or
   missing credentials fails startup. Provider config and credentials still
   require a restart; the enable flag does not.
5. Run the qualification checks below before admitting customers.

### Deploy the branded gateway

The production Ansible path keeps this optional until a backend is qualified.
Set the following inventory values on the control-plane host:

```yaml
faas_object_storage_gateway_managed: true
faas_object_storage_gateway_enabled: true
faas_object_storage_config_src: /operator/config/object-storage.json
# S3/R2/OVH only; GCS with attached ADC does not need this file.
faas_object_storage_provider_env_src: /operator/secrets/object-storage.env
```

The `s3_gateway_service` role installs the hardened systemd unit, bounded spool
directory, provider registry, optional root-only provider environment file,
the matching apid drop-in, and an isolated Caddy site for `s3.gregale.dev`.
It validates that the public endpoint and signing region remain
`https://s3.gregale.dev` and `us-east-1`, starts the daemon, and requires its
database-aware readiness endpoint on `127.0.0.1:9096` to pass. Prometheus
scrapes `/metrics` only when this role is enabled. Normal releases restart and
health-gate an enabled gateway after switching `/opt/faas/current`.

Before running the role, create a **DNS-only** Cloudflare A/AAAA record for
`s3.gregale.dev` pointing at the public Caddy edge. Do not enable the orange-
cloud proxy for this hostname: Cloudflare request-size and duration ceilings
must not become undocumented Gregale storage limits.

The daemon being healthy does not enable customer storage. Keep the global
`s3_enabled` runtime configuration false until provider qualification passes,
then enable it and run the cleanup-safe branded smoke:

```sh
FAAS_TOKEN=... \
GREGALE_APP_SLUG=storage-smoke \
make object-storage-gateway-smoke
```

The smoke creates a uniquely named bucket and credential, exercises HEAD,
PUT, GET, LIST and DELETE through `s3.gregale.dev`, verifies revocation, then
deletes the credential and bucket. Its exit trap repeats cleanup after a
failure and prints the exact bucket name if provider cleanup still needs
operator attention.

### Publish assets on an app hostname

Create a bucket with `public: true` and a stable `serve_at` path, for example
`{"name":"assets","public":true,"serve_at":"/assets"}`. Once the bucket
is ready, `GET` and `HEAD` requests to
`https://<app>.<apps-domain>/assets/<key>` are served directly by
`gatewayd-public`; a route hit never wakes or proxies to the app. Responses
use `Cache-Control: public, max-age=31536000, immutable` and do not require a
Gregale or S3 signed URL. The mount path is immutable after creation; choose a
new bucket if an app needs a different public path. Public reads still pass
through the configured object-storage accounting policy, and successful bytes
are recorded in the per-bucket request ledger; provider-authoritative egress
reports then appear in `usage/storage`. Set `public: false` (and omit `serve_at`) for the default
private bucket behavior.

### Run the live provider qualification

The repository includes an opt-in qualification test that exercises the
provider-neutral contract against the registry's configured default backend:
bucket creation/deletion, signed single-object PUT/GET, object listing,
multipart initiation and recovery, paginated part listing, completion, and
idempotent abort. It performs real upstream writes and deletes, so use a
dedicated provider project/account and a temporary configuration whose
`defaults` points at the backend being qualified.

```sh
FAAS_OBJECT_STORAGE_CONFIG=/etc/faas/object-storage-qualification.json \
FAAS_OBJECT_STORAGE_LIVE_TEST=1 \
make object-storage-qualify
```

Set `FAAS_OBJECT_STORAGE_TEST_REGION` when qualifying a non-default configured
region. Credentials remain in the environment variables named by the config;
they are never written to the test command, JSON, logs, or test output. A
successful run is evidence that the selected provider meets Gregale's data
contract, not evidence of billing, residency, lifecycle, or account-isolation
behavior. Those launch gates still require their own provider-specific checks.

New signed URLs also require an explicit `accounting` policy, a complete
inventory baseline, and fresh authoritative provider reports. See below;
loading the registry and enabling the flag alone is no longer sufficient.

## Hot enable / disable

The single global runtime-config key `s3_enabled` defaults to **false**, even
when a provider registry is loaded. Through the existing authenticated operator
configuration API, use `PATCH /v1/admin/config/s3_enabled` with
`{"value":true,"reason":"enable qualified storage backend"}`; set `value` to
`false` to disable. Use `expected_version` for optimistic concurrency as with
other runtime settings. Existing operator authorization, audit, and rollback
rules apply. No additional environment enable flag or account allowlist exists.

The existing database notification subscriber propagates changes across API
and S3 gateway replicas, with a five-second repair poll for missed notifications
while the DB is reachable. This is not a synchronous global revocation barrier.
In-flight operations can finish, and already-issued internal provider requests
remain usable only inside the gateway until their short expiration.
Disabling blocks new bucket provisioning, GET/PUT URL issuance, multipart
initiation and part-URL issuance, and pauses background provisioning. Bucket
metadata, object listing, object deletion, empty-bucket deletion, multipart
completion/abort, and expired-upload cleanup remain available under their
existing authorization rules. Background deletion also continues. Keep the
provider config/credentials loaded for cleanup. The bucket-list endpoint reports
`enabled: false` while still returning metadata and configured limits. Enabling
without a loaded registry does not make storage usable.

Rollout: apply the recovery migration, then update every apid replica before
relying on this flag. Older binaries treat a loaded registry as enabled and do
not honor `s3_enabled`; keep customer storage traffic disabled during a mixed-
version rollout. Before rollback, disable signing and the branded endpoint,
abort or finish every live
multipart session, wait out issued URLs, restore `max_upload_bytes` to at most
5 GiB, and verify no capacity grant exceeds 5 GiB. Then stop recovery workers
before rolling the schema back; the down migration deliberately refuses to
discard a larger safety reservation silently.

## Branded S3 endpoint

Gregale-issued S3 credentials are bucket-scoped and use the stable customer
contract below, regardless of whether the bucket is placed on OVH, R2, GCS, or
a future Gregale-owned storage cluster:

- endpoint: `https://s3.gregale.dev`
- signing region: `us-east-1`
- addressing: path-style only (`https://s3.gregale.dev/{bucket}/{key}`)

Path-style is intentional. The available `*.gregale.dev` certificate covers
`s3.gregale.dev`, but it does not cover bucket hosts such as
`assets.s3.gregale.dev`. Create a DNS-only Cloudflare record for
`s3.gregale.dev` during the initial rollout so Cloudflare's proxy upload-size
and request-duration limits are not accidentally presented as Gregale storage
limits. Caddy terminates TLS and forwards this hostname to s3-gatewayd on
`127.0.0.1:8084`; preserve the original Host header. Do not share the
`api.gregale.dev` reverse-proxy route, request-body limits, or auth middleware.

Create a credential with
`POST /v1/apps/{slug}/buckets/{bucket-id}/s3-credentials` and a body such as
`{"label":"laptop","permission":"read_write"}`. The response contains the
access key ID, secret access key, endpoint, region, and addressing style. The
secret is returned once. List active credentials with `GET` on the same path
and revoke one with `DELETE .../s3-credentials/{credential-id}`. Revocation is
checked from Gregale's database on every new request.

For an AWS CLI profile, store the returned credentials through the CLI's normal
credential mechanism, then set:

```sh
aws configure set profile.gregale.region us-east-1
aws configure set profile.gregale.s3.addressing_style path
aws --profile gregale --endpoint-url https://s3.gregale.dev \
  s3api list-objects-v2 --bucket assets
```

### Bind storage to a compute workload

Use `POST /v1/apps/{slug}/buckets/{bucket-id}/compute-bindings` when the
workload should use the branded S3 endpoint without carrying credentials in
deployment manifests. The request accepts the same `permission` values as a
standalone credential and an optional uppercase `prefix`. Gregale creates one
bucket-scoped credential and writes six sealed app secrets under that prefix:
`ENDPOINT`, `REGION`, `BUCKET`, `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY`, and
`ADDRESSING_STYLE`. The workload receives them through the existing secret
staging path on its next deploy/wake; values are never returned by the binding
API or stored in plaintext.

List bindings with `GET .../compute-bindings`, rotate in place with
`POST .../compute-bindings/{binding-id}/rotate`, and revoke with
`DELETE .../compute-bindings/{binding-id}`. Rotation keeps secret names stable
and immediately invalidates the previous access key. Revocation invalidates
the credential first, then removes the managed app secrets. Ordinary secret
PUT/DELETE calls cannot overwrite or remove a managed binding secret.

Compute remains stateless: this binding supplies S3 SDK configuration, not a
persistent filesystem mount. The app must still have outbound access to
`s3.gregale.dev` under its egress policy.

### Release gates and staging smoke

Run the read-only release preflight before promoting a release. It checks that
the provider registry is loaded and enabled, limits/regions are non-zero, and
the compute-binding routes are present in the deployed binary:

```sh
FAAS_TOKEN=... \
GREGALE_APP_SLUG=disposable-storage-smoke \
GREGALE_API_URL=https://api.gregale.dev \
make object-storage-release-preflight
```

After the preflight passes, run the mutating qualification against the same
disposable app. It creates a uniquely named bucket, exercises direct S3
object I/O, creates and rotates a compute binding, verifies that the managed
secret names remain stable while the access key changes, revokes both
credentials, and deletes the bucket on every exit path:

```sh
FAAS_TOKEN=... \
GREGALE_APP_SLUG=disposable-storage-smoke \
GREGALE_API_URL=https://api.gregale.dev \
make object-storage-gateway-smoke
```

The smoke test requires `aws`, `curl`, and `jq`. It never prints credential
material or signed URLs. Use a disposable app and do not run it against a
customer bucket; the cleanup trap removes the temporary object, bindings,
credential, and bucket even when a check fails.

This first endpoint slice supports ListBuckets for the credential's one bucket,
HeadBucket, GetBucketLocation, ListObjectsV2 with delimiter/common-prefix
listing,
GetObject/HeadObject/PutObject/DeleteObject, and the standard multipart
initiate/list-parts/upload-part/complete/abort operations, plus CopyObject with
COPY/REPLACE metadata and tagging directives. It validates AWS
Signature V4 in both the `Authorization` header and presigned query form.
Presigned GET, HEAD, PUT, and DELETE capabilities are limited to seven days and
remain subject to credential revocation when a request arrives. Uploads
validate SHA-256 and Content-MD5 before writing upstream. Ordinary PUT accepts
the standard HTTP metadata fields, `x-amz-meta-*`, and URL-encoded
`x-amz-tagging`; GET/HEAD returns customer metadata using the branded
`x-amz-meta-*` names. `?tagging` supports GET, PUT, and DELETE with up to ten
tags per object. S3 backends use native tags; GCS stores the tag set in a
reserved provider-private metadata field (including direct signed uploads) that
is hidden from customers. It emits
Gregale-owned S3 XML errors and filters provider response headers, URLs, bucket
names, and credentials. At most four PUTs per gateway are staged concurrently;
additional authenticated uploads receive S3 `SlowDown` without consuming more
spool disk. Multipart parts are streamed through the gateway to the selected
provider and are limited by the configured per-part upload ceiling. Use
`s3api put-object` for simple uploads; the high-level `aws s3 cp` command can
automatically select multipart uploads.

SigV4 streaming/chunked uploads, bucket lifecycle APIs, versioning, ACLs, and
bucket create/delete through the S3 protocol are explicit `NotImplemented`
gaps. Bucket lifecycle remains on the authenticated Gregale API so a customer
credential cannot escape its assigned logical bucket.

## Recovery and operator attention

Each production apid runs a recovery sweep at startup and every 15 seconds after
the preceding sweep completes. Each batch selects at most 20 due operations;
replicas atomically claim the persisted two-minute leases before upstream I/O.
Bucket operations have a 45-second deadline and multipart operations a 90-second
deadline. The worker only retries catalogued bucket/upload intents, never
discovers or deletes an unknown bucket or an upload without a durable Gregale
session.

Failed requests and background attempts persist `attempt_count`, `retry_at`, and
a bounded `last_error_code`. Transient failures back off from 30 seconds to 15
minutes; invalid requests and credential/configuration failures retry once per
hour. Request retries respect the same cooldown (409 while busy/not due).
Cleanup may replace a failed provisioning intent once its active lease ends.
Successful completion resets retry metadata. Nonempty deletion returns the
bucket to `ready` rather than repeatedly trying to delete customer data.

After a process crash, recovery waits for the lease to expire and repeats the
same operation against the same physical bucket. Missing buckets on deletion
are success; successful creation followed by a lost response must be safe to
repeat on the qualified provider. A stale lease owner cannot commit a newer
worker's outcome. Provider qualification remains necessary for external effects.

Inspect `faas_object_storage_recovery_attempts_total{operation,outcome}` for
worker progress (`success`, `not_empty`, `deferred`). Structured logs contain
bucket/backend IDs, operation, attempt, retry interval, and sanitized error code;
`needs_attention=true` marks configuration/invalid failures or five consecutive
attempts. No raw upstream errors, credentials, or signed URLs are recorded.
The durable retry fields are available for operator diagnosis in the bucket
catalog; they are not added to the customer API. A failed sweep emits a warning.

For configuration failures, restore the original backend identity and valid
credentials on all replicas; restart for provider-config changes and wait for
the persisted retry time. Do not bypass placement fencing or mutate lease tokens
to force recovery. The first release does not include a manual force-retry API,
ready-bucket inventory/orphan reconciliation, or automatic data migration.

The identity needs bucket creation/deletion and CORS configuration; object
list/get/put/delete; and multipart list/create/upload-part/complete/abort/head for
Gregale buckets. GCS additionally requires the IAM Service Account Credentials
API plus `iam.serviceAccounts.signBlob` on `gcs_service_account`; grant that
permission to the ADC principal without exporting a private key. Restrict the
identity to `gregale-*` where supported; otherwise isolate the upstream project.
Configure the provider's abort-incomplete-multipart
lifecycle rule as a backup with a window longer than Gregale's 24-hour session
TTL. Enable provider/account public-access blocking where available. The driver
creates buckets without public ACLs, but does not manage provider-specific
account policies, lifecycle rules, encryption keys, residency controls,
retention, or replication. Keep versioning and object lock off for this preview;
the UI does not manage historical versions or retention locks.

Bucket names in Gregale are logical and app/scope-local. Physical names are
UUID-based to avoid leaking customer identifiers or colliding across providers.
Only configured region defaults appear in the creation catalog.

## Accounting and safety budgets

The `accounting` object in the same provider-registry JSON sets uniform
operator limits. It does not add an enable flag or account allowlist. Missing
or null policy keeps metadata/cleanup usable but blocks new signed URLs.
Policy changes require restarting API replicas with identical config.

Example safety values **only**, not approved pricing or plan allowances:

```json
"accounting": {
  "max_account_bytes": 10737418240,
  "max_bucket_bytes": 5368709120,
  "max_account_keys": 100000,
  "max_monthly_cost_millicents": 500000,
  "max_monthly_requests": 1000000,
  "max_monthly_egress_bytes": 10737418240,
  "max_monthly_authorizations": 100000,
  "max_report_age_seconds": 7200
}
```

Costs use EUR millicents: 1000 millicents = 1 cent. These are ceilings on
reported upstream cost, not customer invoice rates. Every limit must be
positive; zero is not an unlimited setting. Report freshness must be 60–86400
seconds and the key ceiling at most one million. OVH access-log-backed
reporting requires at least 7200 seconds to allow for the provider's normal
one-hour delivery lag and the five-minute export interval.

An optional `pricing` object can add a provider-neutral customer rate card
without changing the safety policy:

```json
"pricing": {
  "currency": "EUR",
  "storage_millicents_per_gib_month": 15000,
  "requests_millicents_per_million": 500,
  "egress_millicents_per_gib": 90000
}
```

`GET /v1/account/object-storage-usage` then includes `charges` with the
storage, request, egress and total estimate for the current UTC month. Storage
uses a 730-hour month; request and egress units are rounded up independently
to one millicent. Omit `pricing` while qualifying providers to keep the
existing usage-only response. This rate card is intentionally separate from
upstream `cost_millicents`; a later billing-provider adapter must post invoice
line items and apply any plan allowances before paid launch.

Before issuing PUT, an account-serialized transaction reserves the maximum
authorized size for its bucket/key and one key slot. Reissuing the same size
or a smaller size does not reserve bytes again. The reservation is committed
before signing and is not refunded on signer errors or lost HTTP responses.
GET and PUT both consume a separate monthly authorization count, used only
for issuance abuse protection—not as a count of actual upstream requests.

Capacity is **conservative**, not a bill: the first inventory baseline plus
per-key grants, or the latest observed bytes/keys, whichever is larger. An
overwrite of a pre-existing baseline key may reserve its size again. Deleting
an object, letting a URL expire, or observing an empty bucket does not reclaim
granted capacity: an accepted in-flight PUT may finish later. Confirmed bucket
deletion releases capacity; empty/delete/recreate the bucket to reclaim it in
this version. Do not manually edit counters. A non-destructive quiescence and
capacity-rebase workflow remains deferred.

Every apid runs a bounded inventory worker at startup and once a minute after
the previous sweep. It claims up to ten ready buckets, with two-minute leases,
a 45-second scan deadline and at most 1000 pages of 1000 keys. Buckets are due
every five minutes. Only complete scans publish a durable observation/sample;
failed/partial/cyclic scans preserve the last observation. Inventory older
than 15 minutes blocks new URLs. Large inventories that cannot complete inside
these bounds fail closed and need a qualified inventory adapter before launch.

The data protocol cannot provide portable request/egress billing. Configure
each backend's optional `usage_reports_path` to an **absolute, operator-owned
regular JSON file**, readable by apid but not writable by customer workloads.
A provider-specific exporter must atomically replace this file with an array
of `ObjectStorageUsageReport` records from actual provider data. Apid imports
it each accounting sweep. Files are capped at 4 MiB / 10,000 reports. Keep
exports limited to the latest cumulative report per account/month/backend.
Publish the same feed to all API replicas, or configure a designated importer.

Each record includes `account_id`, `backend_id`, `backend_fingerprint`, a stable
`source`, UTC `period_start` (first of month), `observed_at` (provider coverage
time, **not export time**), `stored_byte_hours`, actual `request_count`, actual
`egress_bytes`, and `cost_millicents`. Attribute costs using the catalog's
physical bucket/account mapping. The source must cover storage, requests,
egress, and applicable provider charges in the declared EUR cost convention;
do not import an account-total into each tenant or treat delayed/missing data
as zero. Neither Gregale's compute MB-seconds nor inventory samples substitute
for these billing quantities.

The repository includes an OVH Public Cloud adapter in
`pkg/objectstorage/ovh_usage.go`. It reads the provider's signed usage-history
API and normalizes bucket storage byte-hours, outgoing bandwidth, and provider
costs when the provider response is denominated in EUR. A non-EUR project is
rejected until an explicit operator FX/conversion policy exists. OVH's public
usage response does not currently include a request count, so the branded
gateway records every outbound provider request attempt in the durable
`object_storage_request_metrics` ledger. For GA, the OVH runner reads OVH
Server Access Logging from the configured operator bucket per physical bucket
and month, which also covers direct provider URLs; it fails closed when a log
object is missing, malformed, partial, or unknown. It never substitutes
signed-URL issuance counts or an explicit zero for unavailable data. The
catalog and request source are injected through narrow interfaces so a
qualified R2, AWS, GCS, or storage-node adapter can replace OVH without
changing this report contract.

Provider adapters may implement the `objectstorage.UsageReportExporter` seam
and publish through `objectstorage.ExportUsageReports`. The helper validates
that a batch belongs to one backend and UTC period, rejects duplicate account
rows, and atomically replaces the configured report file with owner-only
permissions. This keeps provider credentials, billing APIs, and attribution
logic outside the data drivers; the adapter remains responsible for
obtaining authoritative data and mapping each physical Gregale bucket to one
account.

For an OVH backend, set `usage.driver` to `ovh`, provide the three OVH API
credential environment-variable names, and configure `request_log_bucket` (and
optionally `request_log_prefix`) for a dedicated same-region bucket receiving
[OVH Server Access Logging](https://docs.ovhcloud.com/en/guides/storage-and-backup/object-storage/s3-server-access-logging)
from every managed physical bucket. Keep
`usage_reports_path` in the operator-owned registry. `s3-gatewayd` runs the
exporter immediately and every five minutes, atomically replaces that file, and
exposes `s3_gateway_usage_exports_total` plus the last-success timestamp on its
control-plane metrics endpoint. OVH access logs are delivered asynchronously
(normally about one hour later), so the report's `observed_at` is intentionally
one hour behind the wall clock. Missing credentials, an unreadable/malformed
log object, or a failed export does not publish a partial report; `apid`
therefore continues to enforce the configured freshness window and fails closed
when the previous report ages out. The durable request ledger remains useful
for gateway diagnostics, but access logs are authoritative because API-issued
direct S3 URLs bypass the gateway.

All fields are required, including explicit zero measurements. Reports must
match catalogued backend placement. Identical repeats are harmless;
conflicting duplicates, future observations, decreasing counters/costs, or
changed source identity within a month are rejected. Older monthly evidence
is retained. Corrections reducing totals need a future adjustment workflow.
For manual import, `POST /v1/admin/object-storage/usage-reports` accepts one
record using the existing operator session, recent step-up, allowlist and
Idempotency-Key policy. Normal customer or operator bearer keys cannot import.

`GET /v1/account/object-storage-usage` requires usage-read scope and returns
observed bytes, reserved capacity, cumulative reported usage/cost, authorization
count, policy, and `fresh`. It does not expose credentials or backend placement.
Do not interpret zero counters with `fresh: false` as measured zero usage.
At month rollover, a fresh new-month report is required rather than silently
resetting to an unknown zero. Deleted buckets' monthly costs remain counted.

New URLs return 503 `object_storage_usage_stale` when policy or observations
are unavailable, 402 `object_storage_budget_reached` at cost/request/egress or
authorization ceilings, and PUT returns 409 `object_storage_capacity_reserved`
when a capacity reservation would exceed a limit. Limit errors include
`limit`, `observed`, and a documentation link. Cleanup remains authorized and
available when budgets or the global flag block new URLs.

Monitor `faas_object_storage_inventory_scans_total{outcome="success|failed"}`,
inventory-sweep/provider-import warnings, usage freshness and admission errors.
Logs omit provider error strings, credentials, signed URLs and object keys.

**These are delayed cutoffs, not a hard money cap.** Report latency, sweep
cadence, up to 15 minutes of URL validity, and already-started transfers permit
overshoot; there is no bounded monetary overshoot. Stored data continues to
accrue cost, and permitted cleanup can incur requests after cutoff. Configure
provider budgets/alerts as an additional layer; do not promise that disabling
signing stops the provider bill.

Rollout: keep `s3_enabled=false`, apply migrations, deploy every API replica,
and ensure no old unaccounted URLs or in-flight writes remain before building
the initial baseline. Disabling alone does not end an in-flight transfer;
use the qualified provider's quiescence procedure. Configure/verify the real
usage exporter and explicit limits, confirm fresh observations, then enable.
Rollback must disable signing first; old binaries bypass these guards.
Do not drop accounting tables while serving customer storage.

No plan allowances or invoice lines are introduced by the accounting surface.
The optional rate card is an estimate only; compute billing is unchanged. A
`apid` closes the prior UTC month once per deployment period and stores an
immutable per-account billing snapshot. A billing provider may implement the
optional line-item sink to publish that snapshot idempotently; without one,
the snapshot remains an internal preview ledger. Provider usage exporters,
plan allowances, invoice lines, and month-end reconciliation are still
required for paid launch; see [ADR-156](adr/156-object-storage-accounting.md).

## Provider configuration

Use `driver: "s3"` for S3-compatible services and `driver: "gcs"` for native
Google Cloud Storage. `region` is Gregale's product region. `s3_region` is only
an S3 signing/location setting; `gcs_location` is the GCS bucket placement. A
matching product-region name is not evidence of physical colocation.

| Backend | Endpoint / signing region | Qualification notes |
| --- | --- | --- |
| Google Cloud Storage | Native endpoint, e.g. `EUROPE-WEST3` bucket location | Uses ADC/OAuth for control operations and IAM `signBlob` for V4 URLs; no HMAC or downloaded service-account key. XML multipart preserves the common 5 TiB/10,000-part contract. |
| OVH US Virginia | `https://s3.us-east-va.io.cloud.ovh.us`, `us-east-va` | Example targets the US S3 service, not the legacy Swift endpoint. Verify project availability and selected storage class. |
| AWS S3 Northern Virginia | `https://s3.us-east-1.amazonaws.com`, `us-east-1` | The driver omits CreateBucket LocationConstraint in this region. Use dedicated IAM permissions and account-level public-access blocking. |
| Cloudflare R2 | `https://<account-id>.r2.cloudflarestorage.com`, `auto` | Set `path_style: true`; account token must permit bucket management. R2's placement is not an AWS-style us-east-1 residency guarantee. |
| Your Ceph RGW / other S3 cluster | Your externally reachable HTTPS endpoint and configured zonegroup region | Set `path_style` to match the deployment. Verify TLS, privacy, CORS, signatures and failure behavior. Gregale does not provision the cluster. |

The example endpoint follows [OVH's endpoint guide](https://support.us.ovhcloud.com/hc/en-us/articles/10667991081107-Object-Storage-Endpoints-and-geoavailability).
R2 supports this subset, including CreateBucket, PutBucketCors, ListObjectsV2 and
PutObject Content-MD5, with `auto` as its signing region; see
[R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/).
These are configuration targets, not a claim of completed live-provider testing.
Other providers may require another factory while preserving the same Gregale
API. Do not equate a common object-transfer protocol with identical IAM,
retention or billing APIs.

Only explicit HTTPS origins are accepted for CORS. For isolated local development,
`allow_http: true` permits HTTP endpoints/origins. Production presigned URLs must
be reachable from customers' browsers and app networks; an internal-only endpoint
does not work. Configure the Gregale app's egress rules for its chosen upstream
as necessary. CORS is not authorization: a signed URL grants access to its holder.
Origins are applied during bucket provisioning, not continuously reconciled.
Changing the console origin requires updating existing buckets' CORS through
the operator/provider tools as well as this configuration.

## API quick reference

All paths are under `/v1/apps/{slug}/buckets`, authenticated using existing
Gregale sessions/API keys and MFA policy. Bucket lifecycle and grant management
require `storage:manage`; object reads require `storage:read`; object writes
require `storage:write`. `admin` and dashboard sessions retain full access.

For a non-admin data key, a scope is necessary but not sufficient: the key must
also have an explicit grant on the target bucket. A `read` grant permits object
listing and GET URL issuance, `write` permits object deletion and PUT URL
issuance, and `read_write` permits both when the key carries both scopes.
`storage:manage` does not imply data access. A storage read/write key sees only
its granted buckets in the bucket list; a management principal sees all buckets.

- `GET /`: bucket list and configured limits/regions, across environment scopes.
- `POST /`: `{ "name": "assets", "scope": "default", "region": "us-east-1" }`.
  Omit scope/region for defaults. Retry the same name/scope after failed setup.
- `DELETE /{bucket-id}`: empty-only deletion. Nonempty returns 409; success 204;
  already deleted returns 404. Delete all buckets before deleting the app.
- `GET /{bucket-id}/access-grants`: list the bucket's API-key grants.
- `PUT /{bucket-id}/access-grants/{key-id}`: create or replace a grant with
  `{ "permission": "read" }`, `write`, or `read_write`. The target key must
  carry the corresponding storage scope(s). Admin keys do not need grants.
- `DELETE /{bucket-id}/access-grants/{key-id}`: revoke the grant. Already-issued
  URLs remain valid only until their short expiry.
- `GET /{bucket-id}/objects?prefix=folder%2F&limit=100&cursor=...`: one page;
  pass `next_cursor` without interpreting it, keeping the same prefix.
- `DELETE /{bucket-id}/objects?key=...`: URL-encode the entire exact key.
- `POST /{bucket-id}/signed-url`: `{ "method": "PUT", "key": "hello.txt",
  "size_bytes": 5, "content_type": "text/plain", "metadata": {"owner":"platform"},
  "tags": {"env":"prod"}, "expires_in": 300 }`. The returned headers must
  be preserved exactly for the upload.
  Use returned `url`, `method`, `headers` with the exact five-byte body. For
  download request `{ "method": "GET", "key": "hello.txt" }` (read scope).
- `POST /{bucket-id}/multipart-uploads`: create or recover one upload for a key
  with `{ "key": "large.bin", "size_bytes": 73400320,
  "content_type": "application/octet-stream" }`. The response gives Gregale's
  opaque upload `id`, exact `part_size_bytes`, `part_count`, and 24-hour expiry.
- `POST /{bucket-id}/multipart-uploads/{upload-id}/parts/{part}/signed-url`:
  `{ "expires_in": 300 }`. Upload that numbered part using the returned headers
  and record the provider's response `ETag`. Parts may be retried and uploaded
  concurrently. Every non-final part has the advertised fixed size; the final
  part may be smaller.
- `POST /{bucket-id}/multipart-uploads/{upload-id}/complete`: send every ETag in
  ascending order as `{ "parts": [{"part_number":1,"etag":"..."}] }`.
  `GET /{bucket-id}/multipart-uploads/{upload-id}` recovers session state after a
  lost API response. `DELETE` on that path aborts an unfinished session.

Use ordinary fetch/HTTP for signed URLs, **not** the authenticated Gregale client.
Never forward Gregale Authorization/cookies. Browsers set Content-Length from
the File body; preserve other returned headers, including Content-MD5 for empty
uploads. URLs default to five minutes and allow at most fifteen; they may be
reused until expiry and PUT replaces an existing key. Do not log or persist them.
Changing app permissions does not revoke previously issued URLs.

Multipart sessions reserve the declared final size before upstream initiation
and use the same `storage:write` scope plus bucket write grant as ordinary PUT.
Only one live session exists per bucket/key; retry creation with the same key,
size and content type to recover its Gregale ID. A different shape conflicts.
Gregale never exposes the provider upload ID. Completion ETags are durably stored
before the upstream call, making completion restart-safe. Sessions expire after
24 hours; the recovery worker aborts expired upstream parts even while new
object-storage operations are disabled. The upstream lifecycle rule is still
required as a defense against control-plane outages.

Key rotation copies bucket grants to the successor so applications can switch
credentials during the normal grace window. For compute workloads, prefer the
compute-binding API above so Gregale owns the sealed credential lifecycle;
standalone credentials remain available for laptops, CI, and external clients.
Gregale never gives a workload the operator's upstream provider credential.

## Switch providers without rewriting the product

Add a second backend with a new immutable `id` and `namespace`, then change
`defaults.us-east-1` to that ID. New buckets use it. Existing buckets retain
their recorded backend; keep its configuration and credentials available.
Endpoint, provider placement, driver or namespace changes fence existing buckets
with 503 instead of redirecting them. Rotate keys within the same namespace by
changing secret values and restarting, not by changing the backend identity.

There is intentionally **no automatic existing-bucket migration**. A future
migration worker must stop new writes/signing, wait out issued URLs, copy and
verify objects/metadata, explicitly update placement, and retain rollback data.
The registry and durable placement remove the API/UI rewrite, not the cost or
operational risk of moving bytes. Native customer S3/HMAC credentials require a
separate tenant IAM adapter; never hand out the operator-wide credential.

## Qualification and launch checklist

- Create a bucket; retry creation and verify only one upstream bucket exists.
- Check unauthenticated list/get/put are denied. Check another Gregale account
  cannot list, sign, delete, or guess access to this bucket.
- Upload/download a Unicode/spaced key, an empty object, and a file near the
  configured limit from the actual console origin. Verify exact contents.
- Upload a multipart object larger than 64 MiB with concurrent parts. Retry a
  part, resume by Gregale upload ID, interrupt apid before and after upstream
  completion, verify exact bytes/metadata, abort a session, and verify an expired
  session is removed upstream while `s3_enabled=false`.
- Tamper with a nonempty signed upload's Content-Length and an empty upload's
  body: both must fail. Verify wrong key/method and expired URLs fail.
- Test CORS preflight, pagination, deletion of objects, nonempty bucket rejection,
  empty deletion, upstream outage, and automatic recovery after apid interruption
  before/after upstream success and before/after catalog completion. Verify
  cooldowns survive restart and multiple replicas do not duplicate active work.
- Add a second backend and switch the default. Old/new buckets must use their
  respective backends; removing the old config must fail closed.
- Keep operator monitoring/budgets in place. Defaults are 10 buckets/app and
  100 MiB/upload, configurable up to 100 and 5 TiB. A single signed PUT remains
  capped at 5 GiB; larger objects use multipart. These alone do **not** cap total
  bytes or costs; configure and qualify the accounting controls above. Presign
  counts cannot meter actual usage. The optional rate card is only an estimate;
  plan allowances and invoice lines do not ship here.
- Before paid/general availability, qualify the real provider usage exporter,
  pricing/margin policy and budget cutoffs, tenant native storage keys if needed,
  and a coordinated account-deletion workflow. Active buckets block account
  hard-deletion; confirmed-deleted bucket metadata is purged with the account.
  Do not bypass these guards and orphan customer data.

Deferred: the S3 compatibility gaps listed above, production edge/service
activation, lifecycle/version management, non-destructive capacity reclamation
and automatic migrations.
