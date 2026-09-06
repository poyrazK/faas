# Customer object storage preview

Gregale can manage private S3-backed object buckets without operating storage
nodes. Compute remains stateless: these are not VM volumes. The dashboard's
Storage page keeps object buckets separate from snapshot/image-layer usage.
Architecture and launch boundaries: [ADR-151](adr/151-provider-neutral-object-storage.md).
Large-upload protocol: [ADR-158](adr/158-provider-neutral-multipart-uploads.md).

## Enable a qualified backend

1. Apply database migrations through the normal Gregale deployment process.
2. Copy [the example](../deploy/object-storage.example.json) to an operator-owned
   config path, e.g. `/etc/faas/object-storage.json`. Set the real namespace,
   endpoint, region, and exact browser origins. Use a dedicated upstream
   account/project, not one containing unrelated infrastructure buckets.
3. Supply the named access/secret environment variables to **apid only**, through
   the deployment's secret mechanism. Never put values in JSON, app envs, source
   control, URLs, or logs. Optional `session_token_env` supports temporary
   upstream credentials; restart/rotate before their expiration.
4. Set `FAAS_OBJECT_STORAGE_CONFIG=/etc/faas/object-storage.json` for apid and
   restart all replicas with identical settings. This loads provider configuration
   but does not enable provisioning or signing. Missing config disables the
   feature. Invalid config or missing credentials fails startup. Provider config
   and credentials still require a restart; the enable flag does not.
5. Run the qualification checks below before admitting customers.

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
replicas, with a five-second repair poll for missed notifications while the DB
is reachable. This is not a synchronous global revocation barrier. In-flight
operations can finish, and already-issued URLs remain usable until expiration.
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
version rollout. Before rollback, disable signing, abort or finish every live
multipart session, wait out issued URLs, restore `max_upload_bytes` to at most
5 GiB, and verify no capacity grant exceeds 5 GiB. Then stop recovery workers
before rolling the schema back; the down migration deliberately refuses to
discard a larger safety reservation silently.

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
Gregale buckets. Restrict it to `gregale-*` where supported; otherwise isolate
the upstream project. Configure the provider's abort-incomplete-multipart
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
  "max_report_age_seconds": 3600
}
```

Costs use EUR millicents: 1000 millicents = 1 cent. These are ceilings on
reported upstream cost, not customer invoice rates. Every limit must be
positive; zero is not an unlimited setting. Report freshness must be 60–86400
seconds and the key ceiling at most one million.

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

The S3 data protocol cannot provide portable request/egress billing. Configure
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
for these billing quantities. **No OVH/AWS/R2 billing exporter is bundled yet**;
the normalized import contract is provider-neutral, and a real exporter is
still a deployment prerequisite.

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

No customer prices, included allowances or invoice lines are introduced.
Compute billing is unchanged. This accounting milestone is not paid-launch
approval; see [ADR-156](adr/156-object-storage-accounting.md).

## Provider configuration

All entries use `driver: "s3"` when the backend meets the contract. `region` is
Gregale's product region; `s3_region` is only an upstream signing/location
setting. A matching name is not evidence of physical colocation.

| Backend | Endpoint / signing region | Qualification notes |
| --- | --- | --- |
| OVH US Virginia | `https://s3.us-east-va.io.cloud.ovh.us`, `us-east-va` | Example targets the US S3 service, not the legacy Swift endpoint. Verify project availability and selected storage class. |
| AWS S3 Northern Virginia | `https://s3.us-east-1.amazonaws.com`, `us-east-1` | The driver omits CreateBucket LocationConstraint in this region. Use dedicated IAM permissions and account-level public-access blocking. |
| Cloudflare R2 | `https://<account-id>.r2.cloudflarestorage.com`, `auto` | Set `path_style: true`; account token must permit bucket management. R2's placement is not an AWS-style us-east-1 residency guarantee. |
| Your Ceph RGW / other S3 cluster | Your externally reachable HTTPS endpoint and configured zonegroup region | Set `path_style` to match the deployment. Verify TLS, privacy, CORS, signatures and failure behavior. Gregale does not provision the cluster. |

The example endpoint follows [OVH's endpoint guide](https://support.us.ovhcloud.com/hc/en-us/articles/10667991081107-Object-Storage-Endpoints-and-geoavailability).
R2 supports this subset, including CreateBucket, PutBucketCors, ListObjectsV2 and
PutObject Content-MD5, with `auto` as its signing region; see
[R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/).
These are configuration targets, not a claim of completed live-provider testing.
Other providers may require a factory with different bucket provisioning/CORS
operations while reusing the S3 data driver. Do not equate “S3-compatible” with
identical IAM, retention or billing APIs.

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
  "size_bytes": 5, "content_type": "text/plain", "expires_in": 300 }`.
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
24 hours; the recovery worker aborts expired upstream parts even while new S3
operations are disabled. The upstream lifecycle rule is still required as a
defense against control-plane outages.

Key rotation copies bucket grants to the successor so applications can switch
credentials during the normal grace window. Store the resulting narrowly scoped
Gregale key through the existing app-secret workflow when a workload needs to
request signed URLs. Gregale does not inject it automatically and never gives a
workload the operator's upstream S3 credential.

## Switch providers without rewriting the product

Add a second backend with a new immutable `id` and `namespace`, then change
`defaults.us-east-1` to that ID. New buckets use it. Existing buckets retain
their recorded backend; keep its configuration and credentials available.
Endpoint, Gregale/signing region, driver or namespace changes fence existing buckets
with 503 instead of redirecting them. Rotate keys within the same namespace by
changing secret values and restarting, not by changing the backend identity.

There is intentionally **no automatic existing-bucket migration**. A future
migration worker must stop new writes/signing, wait out issued URLs, copy and
verify objects/metadata, explicitly update placement, and retain rollback data.
The registry and durable placement remove the API/UI rewrite, not the cost or
operational risk of moving bytes. Native customer S3 keys require a separate
tenant IAM adapter; never hand out the operator-wide credential.

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
  counts cannot meter actual usage. No object-storage prices, allowances or
  invoice lines ship here.
- Before paid/general availability, qualify the real provider usage exporter,
  pricing/margin policy and budget cutoffs, tenant S3 keys if needed, and a
  coordinated account-deletion workflow. Active buckets block account
  hard-deletion; confirmed-deleted bucket metadata is purged with the account.
  Do not bypass these guards and orphan customer data.

Deferred: native S3 credentials/endpoint, public hosting, lifecycle/version
management, non-destructive capacity reclamation and automatic migrations.
